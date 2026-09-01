package traverse

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Frame is one line of a stack trace.
type Frame struct {
	Path string `json:"path"`           // as reported by the runtime — often absolute
	Line int    `json:"line,omitempty"` // 0 when the runtime did not report one
	Func string `json:"func,omitempty"` // enclosing function, when reported
}

// framePattern is one runtime's stack-trace format and the capture groups it fills.
type framePattern struct {
	re   *regexp.Regexp
	file int // capture-group index of the file path
	line int // capture-group index of the line number (0 = not captured)
	fn   int // capture-group index of the function name (0 = not captured)
}

// sourceExts also guards the generic patterns, which would otherwise match
// version strings, IP addresses, and timestamps.
const sourceExts = `py|js|jsx|ts|tsx|mjs|cjs|go|java|rb|php|c|cc|cpp|h|hpp|cs|rs|kt|swift|scala`

// Tried in order, first match wins. Language-specific formats precede the
// generic fallback so a function name is captured whenever one is reported.
var framePatterns = []framePattern{
	// Python:  File "/srv/app/billing/charge.py", line 42, in process_payment
	{re: regexp.MustCompile(`File "([^"]+)", line (\d+)(?:, in ([\w.]+))?`), file: 1, line: 2, fn: 3},

	// Java / Kotlin:  at com.acme.Billing.charge(Billing.java:42)
	{re: regexp.MustCompile(`\bat\s+([\w$.]+)\(([\w$]+\.(?:java|kt)):(\d+)\)`), file: 2, line: 3, fn: 1},

	// Node / V8:  at processPayment (/srv/app/billing/charge.js:42:15)
	//             at /srv/app/billing/charge.js:42:15
	{re: regexp.MustCompile(`\bat\s+(?:([\w$.<>]+)\s+)?\(?([^\s()]+\.(?:` + sourceExts + `)):(\d+):\d+\)?`), file: 2, line: 3, fn: 1},

	// Ruby:  /srv/app/billing/charge.rb:42:in `process_payment'
	{re: regexp.MustCompile("([^\\s:]+\\.rb):(\\d+):in [`']([\\w.?!]+)"), file: 1, line: 2, fn: 3},

	// Go:  /srv/app/billing/charge.go:42 +0x1c   (the preceding line holds the func)
	{re: regexp.MustCompile(`([^\s:()]+\.go):(\d+)`), file: 1, line: 2},

	// Generic "path/to/file.ext:123" — PHP, C/C++, Rust, and log lines that
	// mention a source location without a recognised stack format.
	{re: regexp.MustCompile(`([\w./\\-]+\.(?:` + sourceExts + `)):(\d+)`), file: 1, line: 2},

	// Last resort: a bare source path with no line number, e.g. a log line that
	// names the file that logged it.
	{re: regexp.MustCompile(`([\w./\\-]+\.(?:` + sourceExts + `))\b`), file: 1},
}

// goFuncLine matches the "package.Func(args)" line a Go panic prints above each file:line.
var goFuncLine = regexp.MustCompile(`^([\w./\-]+\.[\w.*()]+)\(`)

// maxFrames bounds a long trace; past the top frames a recursion dump is all noise.
const maxFrames = 25

// ExtractFrames pulls stack frames out of an alert body, throw site first.
func ExtractFrames(errorBody string) []Frame {
	lines := strings.Split(errorBody, "\n")

	// Python prints the deepest frame last; every other runtime prints it first.
	pythonOrder := strings.Contains(errorBody, "most recent call last")

	var (
		frames  []Frame
		seen    = map[string]bool{}
		pending string // a Go panic's function line, consumed by the next frame
	)

	for _, line := range lines {
		if m := goFuncLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			pending = m[1]
			continue
		}

		f, ok := matchFrame(line)
		if !ok {
			continue
		}
		if f.Func == "" && pending != "" {
			f.Func = shortFuncName(pending)
		}
		pending = ""

		// A recursive trace repeats one frame enough to crowd out the rest.
		key := f.Path + ":" + strconv.Itoa(f.Line)
		if seen[key] {
			continue
		}
		seen[key] = true

		frames = append(frames, f)
		if len(frames) >= maxFrames {
			break
		}
	}

	if pythonOrder {
		reverseFrames(frames)
	}
	return frames
}

// matchFrame tries each pattern against one line, returning the first hit.
func matchFrame(line string) (Frame, bool) {
	for _, p := range framePatterns {
		m := p.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		f := Frame{Path: m[p.file]}
		if p.line > 0 && p.line < len(m) {
			f.Line, _ = strconv.Atoi(m[p.line])
		}
		if p.fn > 0 && p.fn < len(m) {
			f.Func = shortFuncName(m[p.fn])
		}
		if f.Path == "" {
			continue
		}
		return f, true
	}
	return Frame{}, false
}

// shortFuncName reduces a qualified name ("com.acme.Billing.charge",
// "main.(*Server).handle") to the bare symbol the graph stores.
func shortFuncName(qualified string) string {
	qualified = strings.TrimSuffix(qualified, "()")
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 && i < len(qualified)-1 {
		qualified = qualified[i+1:]
	}
	return strings.Trim(qualified, "*()")
}

func reverseFrames(f []Frame) {
	for i, j := 0, len(f)-1; i < j; i, j = i+1, j-1 {
		f[i], f[j] = f[j], f[i]
	}
}

// FrameRank indexes a trace's frames by the repo-relative path each resolves to.
type FrameRank struct {
	// pos maps a repo-relative path to its topmost frame index; 0 is the throw site.
	pos    map[string]int
	frames []Frame
}

// RankFrames resolves each frame's reported path against the repo's known paths.
//
// A runtime reports whatever path it was built with ("/app/dist/billing/charge.js")
// while the graph stores "billing/charge.js", so we match on the longest suffix
// owned by exactly one indexed file: that tolerates any build prefix without
// letting a bare "index.js" match a dozen unrelated files.
func RankFrames(frames []Frame, repoPaths []string) *FrameRank {
	fr := &FrameRank{pos: make(map[string]int), frames: frames}
	if len(frames) == 0 || len(repoPaths) == 0 {
		return fr
	}

	// Index every segment-aligned suffix, recording its sole owner.
	const ambiguous = "\x00ambiguous"
	suffixOwner := make(map[string]string)
	for _, p := range repoPaths {
		norm := normalizePath(p)
		for _, suf := range pathSuffixes(norm) {
			switch suffixOwner[suf] {
			case "":
				suffixOwner[suf] = p
			case p:
				// same file, nothing to do
			default:
				suffixOwner[suf] = ambiguous
			}
		}
	}

	for i, f := range frames {
		norm := normalizePath(f.Path)
		// Longest suffix first: the most specific unambiguous match wins.
		for _, suf := range pathSuffixes(norm) {
			owner := suffixOwner[suf]
			if owner == "" || owner == ambiguous {
				continue
			}
			if _, already := fr.pos[owner]; !already {
				fr.pos[owner] = i
			}
			break
		}
	}
	return fr
}

// Paths returns the resolved repo-relative files, throw site first.
func (fr *FrameRank) Paths() []string {
	if fr == nil || len(fr.pos) == 0 {
		return nil
	}
	out := make([]string, 0, len(fr.pos))
	for p := range fr.pos {
		out = append(out, p)
	}
	// Insertion-sort by stack position; the map is small (≤ maxFrames).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && fr.pos[out[j]] < fr.pos[out[j-1]]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Score rates how strongly the trace implicated a file: 1.0 at the throw site,
// decaying with depth, 0 for a file that never appeared.
func (fr *FrameRank) Score(repoPath string) float64 {
	if fr == nil || repoPath == "" {
		return 0
	}
	i, ok := fr.pos[repoPath]
	if !ok {
		return 0
	}
	return 1.0 / float64(i+1)
}

// Position returns the frame index a file appeared at, if it appeared at all.
func (fr *FrameRank) Position(repoPath string) (int, bool) {
	if fr == nil {
		return 0, false
	}
	i, ok := fr.pos[repoPath]
	return i, ok
}

func (fr *FrameRank) frameFunc(i int) string {
	if fr == nil || i < 0 || i >= len(fr.frames) {
		return ""
	}
	return fr.frames[i].Func
}

// Empty reports whether the incident carried no usable stack trace.
func (fr *FrameRank) Empty() bool { return fr == nil || len(fr.pos) == 0 }

// normalizePath makes a reported path comparable: forward slashes, no leading "./".
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.TrimPrefix(p, "./")
	return path.Clean(p)
}

// pathSuffixes returns p's segment-aligned suffixes, longest first:
// "a/b/c.py" → ["a/b/c.py", "b/c.py", "c.py"].
func pathSuffixes(p string) []string {
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for i := range segs {
		out = append(out, strings.Join(segs[i:], "/"))
	}
	return out
}
