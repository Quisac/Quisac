package traverse

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"quisac/internal/core"
)

// FileStat is a file's change and fault history, keyed by repo-relative path.
type FileStat = core.FileStat

// StatsSource supplies per-file history for a repo. Satisfied by *db.Store.
type StatsSource interface {
	FileStats(ctx context.Context, repo string) (map[string]FileStat, error)
}

// Signals are normalised to 0–1 and combined by these weights, which sum to 1.0
// so that a Score reads directly as a confidence.
const (
	wProximity = 0.28 // hops back from the entry point
	wErrorTerm = 0.18 // the symbol is named in the alert body
	wFrame     = 0.16 // the symbol's file appeared in the stack trace
	wRecency   = 0.12 // the file changed recently
	wFault     = 0.10 // the file has been implicated in past incidents
	wChurn     = 0.06 // the file changes often
	wBlast     = 0.05 // how much of the codebase depends on the symbol
	wKind      = 0.05 // a function is more actionable than a file
)

// proximityDecay is the per-hop falloff as the walk moves back through callers.
// Geometric, not 1/depth: that keeps distant-but-recently-changed code in play.
const proximityDecay = 0.7

// recencyHalfLife: the window in which a regression is still plausibly a recent deploy.
const recencyHalfLife = 14 * 24 * time.Hour

// Saturation points: the value at which a signal is considered maxed out.
const (
	churnSaturation = 20 // changes
	faultSaturation = 3  // past incidents
	blastSaturation = 50 // dependents
)

// Signals is the per-candidate breakdown behind Score, persisted and shown in the UI.
type Signals struct {
	Proximity float64 `json:"proximity"`  // 1/depth
	ErrorTerm float64 `json:"error_term"` // named in the alert body
	Frame     float64 `json:"frame"`      // position in the stack trace
	Recency   float64 `json:"recency"`    // decay since last change
	Fault     float64 `json:"fault"`      // past incident involvement
	Churn     float64 `json:"churn"`      // change frequency
	Blast     float64 `json:"blast"`      // dependents
	Kind      float64 `json:"kind"`       // symbol actionability

	// Raw inputs, for display.
	FanIn         int        `json:"fan_in"`
	ChangeCount   int        `json:"change_count"`
	FaultCount    int        `json:"fault_count"`
	FramePosition int        `json:"frame_position"` // -1 when not on the stack
	LastChangedAt *time.Time `json:"last_changed_at,omitempty"`
}

// scoreInput is everything known about one BFS hit before it is scored.
type scoreInput struct {
	name      string
	symType   string
	filePath  string
	depth     int
	fanIn     int
	errorTerm bool
	frames    *FrameRank
	stat      FileStat
	now       time.Time
}

// score combines every signal into a 0–1 suspicion score, its breakdown, and an
// explanation of the components that contributed.
func score(in scoreInput) (float64, Signals, []string) {
	var s Signals

	depth := in.depth
	if depth < 0 {
		depth = 0
	}
	s.Proximity = math.Pow(proximityDecay, float64(depth))

	if in.errorTerm {
		s.ErrorTerm = 1.0
	}

	s.Frame = in.frames.Score(in.filePath)
	if pos, ok := in.frames.Position(in.filePath); ok {
		s.FramePosition = pos
	} else {
		s.FramePosition = -1
	}

	s.Recency = decay(in.stat.LastChangedAt, in.now, recencyHalfLife)
	s.Fault = saturate(in.stat.FaultCount, faultSaturation)
	s.Churn = saturate(in.stat.ChangeCount, churnSaturation)
	s.Blast = logSaturate(in.fanIn, blastSaturation)
	s.Kind = kindScore(in.symType)

	s.FanIn = in.fanIn
	s.ChangeCount = in.stat.ChangeCount
	s.FaultCount = in.stat.FaultCount
	if !in.stat.LastChangedAt.IsZero() {
		t := in.stat.LastChangedAt
		s.LastChangedAt = &t
	}

	total := wProximity*s.Proximity +
		wErrorTerm*s.ErrorTerm +
		wFrame*s.Frame +
		wRecency*s.Recency +
		wFault*s.Fault +
		wChurn*s.Churn +
		wBlast*s.Blast +
		wKind*s.Kind

	return round(total), s, explain(in, s)
}

// explain lists the signals that materially contributed, strongest first.
func explain(in scoreInput, s Signals) []string {
	type reason struct {
		weight float64
		text   string
	}
	var rs []reason

	add := func(w float64, format string, args ...any) {
		if w > 0.001 {
			rs = append(rs, reason{w, fmt.Sprintf(format, args...)})
		}
	}

	switch in.depth {
	case 0:
		add(wProximity*s.Proximity, "the failing symbol itself")
	case 1:
		add(wProximity*s.Proximity, "direct caller of the failing symbol")
	default:
		add(wProximity*s.Proximity, "%d hops back from the failing symbol", in.depth)
	}
	if s.ErrorTerm > 0 {
		add(wErrorTerm*s.ErrorTerm, "named in the alert body")
	}
	if s.FramePosition == 0 {
		add(wFrame*s.Frame, "the stack trace's throw site")
	} else if s.FramePosition > 0 {
		add(wFrame*s.Frame, "frame %d of the stack trace", s.FramePosition)
	}
	if s.Recency > 0.05 && in.stat.LastChangedAt != (time.Time{}) {
		add(wRecency*s.Recency, "changed %s", humanAge(in.now.Sub(in.stat.LastChangedAt)))
	}
	if s.FaultCount > 0 {
		add(wFault*s.Fault, "implicated in %s before", plural(s.FaultCount, "incident"))
	}
	if s.ChangeCount >= 5 {
		add(wChurn*s.Churn, "high churn (%d changes)", s.ChangeCount)
	}
	if s.FanIn >= 3 {
		add(wBlast*s.Blast, "%s depend on it", plural(s.FanIn, "symbol"))
	}

	sort.SliceStable(rs, func(i, j int) bool { return rs[i].weight > rs[j].weight })

	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.text)
	}
	return out
}

// decay returns 1.0 for a fresh timestamp, halving every halfLife, 0 for none.
func decay(at, now time.Time, halfLife time.Duration) float64 {
	if at.IsZero() || at.After(now) {
		if at.IsZero() {
			return 0
		}
		return 1
	}
	return math.Exp(-math.Ln2 * float64(now.Sub(at)) / float64(halfLife))
}

// saturate maps a count onto 0–1, reaching 1 at max.
func saturate(n, max int) float64 {
	if n <= 0 {
		return 0
	}
	if n >= max {
		return 1
	}
	return float64(n) / float64(max)
}

// logSaturate is saturate on a log scale: 1 vs 5 dependents matters more than 45 vs 50.
func logSaturate(n, max int) float64 {
	if n <= 0 {
		return 0
	}
	v := math.Log1p(float64(n)) / math.Log1p(float64(max))
	return math.Min(1, v)
}

// kindScore favours symbols an engineer can edit over names the parser inferred.
func kindScore(symType string) float64 {
	switch symType {
	case "Function", "Method":
		return 1.0
	case "Class":
		return 0.7
	case "File":
		return 0.5
	case "Variable":
		return 0.3
	default: // Module
		return 0.1
	}
}

func round(f float64) float64 { return math.Round(f*10000) / 10000 }

func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "in the last hour"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 14*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	default:
		return plural(int(d.Hours()/(24*7)), "week") + " ago"
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// extractTerms returns de-duplicated, non-trivial tokens from the error body
// and service name, used to find an entry point and to score named candidates.
func extractTerms(errorBody, serviceName string) []string {
	seen := map[string]bool{}
	var terms []string

	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 3 || stopWord[strings.ToLower(s)] {
			return
		}
		if !seen[s] {
			seen[s] = true
			terms = append(terms, s)
		}
	}

	for _, tok := range tokenize(serviceName) {
		add(tok)
	}
	for _, line := range strings.Split(errorBody, "\n") {
		for _, tok := range tokenize(line) {
			add(tok)
		}
	}
	return terms
}

// tokenize splits on the delimiters separating identifiers in paths and traces.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '-', '.', '/', ':', ' ', '\t', '(', ')', '[', ']', '{', '}',
			',', ';', '"', '\'', '`', '#', '@', '=', '<', '>', '|', '\\':
			return true
		}
		return false
	})
}

func tokenSet(terms []string) map[string]bool {
	m := make(map[string]bool, len(terms))
	for _, t := range terms {
		m[strings.ToLower(t)] = true
	}
	return m
}

// stopWord holds tokens common enough to match half the graph.
var stopWord = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "are": true,
	"was": true, "has": true, "at": true, "in": true, "on": true,
	"to": true, "of": true, "a": true, "an": true, "is": true,
	"err": true, "error": true, "line": true, "func": true, "type": true,
	"null": true, "nil": true, "true": true, "false": true, "new": true,
	"got": true, "get": true, "set": true, "run": true, "main": true,
	"self": true, "this": true, "with": true, "from": true, "into": true,
	"call": true, "code": true, "data": true, "file": true, "name": true,
	"none": true, "test": true, "time": true, "value": true, "traceback": true,
	"exception": true, "warning": true, "critical": true, "failed": true,
}
