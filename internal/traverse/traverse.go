// Package traverse turns an alert into a ranked list of code that could have
// caused it: extract stack frames, resolve them to indexed files, find the
// symbols the failure ran through, walk backward over CALLS/IMPORTS/CONTAINS to
// everything that reaches them, and score the results.
//
// The walk itself is delegated to Cypher, whose variable-length path patterns
// execute as BFS natively; only scoring happens in Go.
package traverse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"quisac/internal/core"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Incident is the normalised payload extracted from a PagerDuty webhook.
type Incident struct {
	ID             string // PagerDuty incident ID
	InstallationID string
	Title          string // human-readable title
	ServiceName    string // PD service summary → used to locate the repo entry point
	ErrorBody      string // alert body / stack trace / custom_details
}

// CandidateNode is a symbol identified as a likely root-cause contributor.
type CandidateNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"` // Function | Method | File | …
	Name     string  `json:"name"`
	FilePath string  `json:"file_path"`
	Depth    int     `json:"depth"` // hops back from an entry point (0 = the entry point itself)
	Score    float64 `json:"score"` // 0–1; higher = more suspicious

	// Reasons is Signals in words, strongest first.
	Signals Signals  `json:"signals"`
	Reasons []string `json:"reasons,omitempty"`
}

// Result is the full output of a traversal run.
type Result struct {
	Repo string `json:"repo"`

	// EntryPoint is the primary symbol the failure was traced to; EntryPoints
	// holds every seed the BFS started from, best first.
	EntryPoint  *core.Node   `json:"entry_point"`
	EntryPoints []*core.Node `json:"entry_points,omitempty"`

	// Frames are the stack frames parsed out of the alert, throw site first, and
	// FramePaths are the repo-relative files they resolved to.
	Frames     []Frame  `json:"frames,omitempty"`
	FramePaths []string `json:"frame_paths,omitempty"`

	// Candidates are the ranked root-cause candidates, most suspicious first.
	Candidates []CandidateNode `json:"candidates"`
}

// TopPaths returns the distinct files behind the highest-scoring candidates,
// best first and capped at n. This is the file set handed to the fix stage.
func (r *Result) TopPaths(n int) []string {
	if r == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string

	// Stack-trace files first.
	for _, p := range r.FramePaths {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
			if len(out) >= n {
				return out
			}
		}
	}
	for _, c := range r.Candidates {
		if c.FilePath == "" || seen[c.FilePath] {
			continue
		}
		seen[c.FilePath] = true
		out = append(out, c.FilePath)
		if len(out) >= n {
			break
		}
	}
	return out
}

// Traverser performs incident-driven BFS over the Neo4j graph.
type Traverser struct {
	driver neo4j.DriverWithContext
	stats  StatsSource // optional; nil zeroes the history signals
}

// New returns a Traverser backed by the supplied Neo4j driver.
func New(driver neo4j.DriverWithContext) *Traverser {
	return &Traverser{driver: driver}
}

// WithStats attaches the change/fault history source used by the recency,
// churn, and fault signals. Without it, traversal still ranks on structure.
func (t *Traverser) WithStats(s StatsSource) *Traverser {
	t.stats = s
	return t
}

// Tuning constants for one traversal run.
const (
	defaultMaxDepth = 5
	maxEntryPoints  = 3   // seeds the BFS starts from
	scanLimit       = 300 // rows pulled from Neo4j before scoring
	resultLimit     = 50  // candidates returned
)

// Run ranks the root-cause candidates for an incident in repo, walking at most
// maxDepth hops backward from the symbols the failure ran through.
func (t *Traverser) Run(ctx context.Context, repo string, incident Incident, maxDepth int) (*Result, error) {
	if maxDepth < 1 || maxDepth > 10 {
		maxDepth = defaultMaxDepth
	}

	// Resolve the alert's stack trace against the files we actually indexed.
	frames := ExtractFrames(incident.ErrorBody)
	repoPaths, err := t.filePaths(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("traverse: load file paths: %w", err)
	}
	rank := RankFrames(frames, repoPaths)

	entries, err := t.findEntryPoints(ctx, repo, incident, rank)
	if err != nil {
		return nil, fmt.Errorf("traverse: find entry point: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no matching node for service %q in repo %q", incident.ServiceName, repo)
	}

	// Best-effort: a stats outage degrades ranking, it does not fail the response.
	stats := t.fileStats(ctx, repo)

	candidates, err := t.walkBack(ctx, repo, entries, maxDepth, incident, rank, stats)
	if err != nil {
		return nil, fmt.Errorf("traverse: backward walk: %w", err)
	}

	return &Result{
		Repo:        repo,
		EntryPoint:  entries[0],
		EntryPoints: entries,
		Frames:      frames,
		FramePaths:  rank.Paths(),
		Candidates:  candidates,
	}, nil
}

// fileStats returns an empty map when no source is configured or the lookup fails.
func (t *Traverser) fileStats(ctx context.Context, repo string) map[string]FileStat {
	if t.stats == nil {
		return nil
	}
	stats, err := t.stats.FileStats(ctx, repo)
	if err != nil {
		return nil
	}
	return stats
}

// filePaths returns the manifest that stack frames are resolved against.
func (t *Traverser) filePaths(ctx context.Context, repo string) ([]string, error) {
	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	res, err := session.Run(ctx,
		`MATCH (n:Symbol:File {repo: $repo}) RETURN n.path AS path`,
		map[string]any{"repo": repo})
	if err != nil {
		return nil, err
	}
	var paths []string
	for res.Next(ctx) {
		if p := strVal(res.Record(), "path"); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, res.Err()
}

// findEntryPoints locates the symbols the failure ran through, best first.
//
// A frame is evidence that a file was executing; a term match is only evidence
// that a word appeared in the payload. Terms are the fallback for alerts with no
// trace at all — a threshold breach, a synthetic check, a bare log line.
func (t *Traverser) findEntryPoints(ctx context.Context, repo string, incident Incident, rank *FrameRank) ([]*core.Node, error) {
	if !rank.Empty() {
		entries, err := t.entryPointsFromFrames(ctx, repo, rank)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return entries, nil
		}
	}
	return t.entryPointsFromTerms(ctx, repo, incident)
}

// entryPointsFromFrames maps the topmost resolved frames onto graph symbols,
// preferring the function the runtime named and falling back to the file node.
func (t *Traverser) entryPointsFromFrames(ctx context.Context, repo string, rank *FrameRank) ([]*core.Node, error) {
	paths := rank.Paths()
	if len(paths) > maxEntryPoints {
		paths = paths[:maxEntryPoints]
	}

	funcs := map[string]string{} // repo-relative path → function name
	for _, p := range paths {
		if pos, ok := rank.Position(p); ok {
			funcs[p] = rank.frameFunc(pos)
		}
	}

	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	var entries []*core.Node
	for _, p := range paths {
		// Prefer the named function, falling back to any symbol in the file.
		res, err := session.Run(ctx, `
			MATCH (n:Symbol {repo: $repo, path: $path})
			WITH n,
			     CASE WHEN $fn <> '' AND toLower(n.name) = toLower($fn) THEN 10 ELSE 0 END AS nameHit,
			     CASE n.type
			       WHEN 'Function' THEN 3
			       WHEN 'Method'   THEN 3
			       WHEN 'Class'    THEN 2
			       WHEN 'File'     THEN 1
			       ELSE 0
			     END AS typeScore
			RETURN n.id AS id, n.type AS type, n.name AS name, n.path AS path
			ORDER BY nameHit + typeScore DESC
			LIMIT 1
		`, map[string]any{"repo": repo, "path": p, "fn": funcs[p]})
		if err != nil {
			return nil, fmt.Errorf("frame entry-point query: %w", err)
		}
		if res.Next(ctx) {
			entries = append(entries, nodeFrom(res.Record()))
		}
		if err := res.Err(); err != nil {
			return nil, fmt.Errorf("frame entry-point cursor: %w", err)
		}
	}
	return entries, nil
}

// entryPointsFromTerms handles alerts with no usable stack trace, scoring on
// term overlap, exactness, and how actionable the symbol type is.
func (t *Traverser) entryPointsFromTerms(ctx context.Context, repo string, incident Incident) ([]*core.Node, error) {
	terms := extractTerms(incident.ErrorBody, incident.ServiceName)
	if len(terms) == 0 {
		return nil, fmt.Errorf("no usable terms found in incident (service=%q)", incident.ServiceName)
	}

	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	res, err := session.Run(ctx, `
		UNWIND $terms AS term
		MATCH (n:Symbol {repo: $repo})
		WHERE n.type IN ['Function', 'Method', 'Class', 'File']
		  AND (toLower(n.name) CONTAINS toLower(term)
		    OR toLower(n.path) CONTAINS toLower(term))
		WITH n, term
		WITH n,
		     count(term) AS termMatches,
		     max(CASE WHEN toLower(n.name) = toLower(term) THEN 10 ELSE 0 END) AS exactMatchBonus
		WITH n, termMatches, exactMatchBonus,
		     CASE n.type
		       WHEN 'Function' THEN 4
		       WHEN 'Method'   THEN 4
		       WHEN 'Class'    THEN 2
		       WHEN 'File'     THEN 1
		       ELSE 0
		     END AS typeScore
		RETURN n.id AS id, n.type AS type, n.name AS name, n.path AS path,
		       (termMatches * 2) + exactMatchBonus + typeScore AS score
		ORDER BY score DESC
		LIMIT $limit
	`, map[string]any{"repo": repo, "terms": terms, "limit": maxEntryPoints})
	if err != nil {
		return nil, fmt.Errorf("entry-point query: %w", err)
	}

	var entries []*core.Node
	for res.Next(ctx) {
		entries = append(entries, nodeFrom(res.Record()))
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("entry-point cursor: %w", err)
	}
	return entries, nil
}

// walkBack ranks every symbol reaching an entry point within maxDepth hops, the
// entry points included — the bug is often in the failing function itself.
//
// CONTAINS is what lets the walk cross files at all: call edges are intra-file,
// so the route out is symbol → its file → that file's importers.
func (t *Traverser) walkBack(
	ctx context.Context,
	repo string,
	entries []*core.Node,
	maxDepth int,
	incident Incident,
	rank *FrameRank,
	stats map[string]FileStat,
) ([]CandidateNode, error) {
	startIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		startIDs = append(startIDs, e.ID)
	}

	session := t.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Cypher will not take a parameter as a variable-length bound. The caller
	// validates maxDepth to 1–10, so interpolating it is safe.
	cypher := fmt.Sprintf(`
		MATCH (start:Symbol {repo: $repo})
		WHERE start.id IN $startIDs
		MATCH path = (n:Symbol {repo: $repo})-[:CALLS|IMPORTS|CONTAINS*0..%d]->(start)
		WITH n, min(length(path)) AS depth
		OPTIONAL MATCH (dep:Symbol {repo: $repo})-[:CALLS|IMPORTS]->(n)
		WITH n, depth, count(DISTINCT dep) AS fanIn
		RETURN n.id AS id, n.type AS type, n.name AS name, n.path AS path,
		       depth, fanIn
		ORDER BY depth ASC
		LIMIT $limit
	`, maxDepth)

	res, err := session.Run(ctx, cypher, map[string]any{
		"repo":     repo,
		"startIDs": startIDs,
		"limit":    scanLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("backward-walk query: %w", err)
	}

	errorTokens := tokenSet(extractTerms(incident.ErrorBody, ""))
	now := time.Now()

	var candidates []CandidateNode
	for res.Next(ctx) {
		rec := res.Record()
		name := strVal(rec, "name")
		filePath := strVal(rec, "path")

		total, signals, reasons := score(scoreInput{
			name:      name,
			symType:   strVal(rec, "type"),
			filePath:  filePath,
			depth:     intVal(rec, "depth"),
			fanIn:     intVal(rec, "fanIn"),
			errorTerm: errorTokens[strings.ToLower(name)],
			frames:    rank,
			stat:      stats[filePath],
			now:       now,
		})

		candidates = append(candidates, CandidateNode{
			ID:       strVal(rec, "id"),
			Type:     strVal(rec, "type"),
			Name:     name,
			FilePath: filePath,
			Depth:    intVal(rec, "depth"),
			Score:    total,
			Signals:  signals,
			Reasons:  reasons,
		})
	}
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("backward-walk cursor: %w", err)
	}

	// Break ties on proximity so ordering does not depend on Neo4j's row order.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Depth < candidates[j].Depth
	})

	if len(candidates) > resultLimit {
		candidates = candidates[:resultLimit]
	}
	return candidates, nil
}

func nodeFrom(rec *neo4j.Record) *core.Node {
	return &core.Node{
		ID:   strVal(rec, "id"),
		Type: strVal(rec, "type"),
		Name: strVal(rec, "name"),
	}
}

func strVal(rec *neo4j.Record, key string) string {
	v, _ := rec.Get(key)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intVal(rec *neo4j.Record, key string) int {
	v, _ := rec.Get(key)
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
