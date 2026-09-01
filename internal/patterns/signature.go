// Package patterns recognises an incident already solved. The same bug recurs
// with a new incident ID, timestamp, and request IDs each time, so a fix is
// recorded against the shape of an error rather than its literal text.
//
// That shape is a Signature: the error's class and message with every volatile
// substring masked out, plus the top stack frame and the service.
package patterns

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"quisac/internal/traverse"
)

// Signature is the stable identity of an error, independent of the run that
// produced it.
type Signature struct {
	// Fingerprint is the exact-match key: sha256 of Text.
	Fingerprint string `json:"fingerprint"`
	// Text is the normalised signature, kept readable so a pattern can be audited.
	Text string `json:"text"`
	// Tokens back fuzzy matching, for shapes that are close but not identical.
	Tokens []string `json:"tokens"`
}

// masks strip the parts of an error that carry no diagnostic weight — request
// IDs, timestamps, ports, addresses — so two runs of one bug hash alike.
//
// Order matters: the broadest patterns (bare numbers) must run last, or they
// chew through the structure the earlier ones rely on.
var masks = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`https?://[^\s"'<>)]+`), "<url>"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<uuid>"},
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<ts>"},
	{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`), "<addr>"},
	{regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`), "<hex>"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{16,}\b`), "<hex>"},
	{regexp.MustCompile(`"[^"\n]{0,200}"`), "<str>"},
	{regexp.MustCompile(`'[^'\n]{0,200}'`), "<str>"},
	{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ms|s|m|h|kb|mb|gb|b)\b`), "<dur>"},
	{regexp.MustCompile(`\b\d[\d,._]*\b`), "<n>"},
}

// errorLine matches the line that names the failure: an exception class and its
// message, a Go panic, or a Node uncaught error.
var errorLine = regexp.MustCompile(
	`(?:^|\s)((?:[A-Za-z_][\w.]*\.)?[A-Z][A-Za-z0-9_]*(?:Error|Exception|Fault|Failure|Panic|Timeout|Refused))\b\s*:?\s*(.*)$`)

var panicLine = regexp.MustCompile(`(?i)^\s*(panic|fatal(?: error)?|uncaught(?: \w+)?)\s*:\s*(.*)$`)

// Analyze reduces an incident to its Signature. frames may be empty: an alert
// without a trace still fingerprints on its masked message and service.
func Analyze(title, errorBody, serviceName string, frames []traverse.Frame) Signature {
	class, message := extractError(title, errorBody)

	var parts []string
	if s := strings.ToLower(strings.TrimSpace(serviceName)); s != "" {
		parts = append(parts, "svc="+s)
	}
	if class != "" {
		parts = append(parts, "err="+strings.ToLower(class))
	}
	if m := mask(message); m != "" {
		parts = append(parts, "msg="+m)
	}
	// Line numbers are excluded, so the bug keeps its identity when the file
	// above it grows by a line.
	if len(frames) > 0 {
		top := frames[0]
		site := normalizeFramePath(top.Path)
		if top.Func != "" {
			site += "::" + strings.ToLower(top.Func)
		}
		parts = append(parts, "at="+site)
	}

	text := strings.Join(parts, " | ")
	sum := sha256.Sum256([]byte(text))

	return Signature{
		Fingerprint: hex.EncodeToString(sum[:]),
		Text:        text,
		Tokens:      tokensFor(text, frames),
	}
}

// extractError finds the class and message of the failure. Python prints the
// exception last; every other runtime prints it first, so the scan follows suit.
func extractError(title, errorBody string) (class, message string) {
	lines := strings.Split(title+"\n"+errorBody, "\n")
	pythonOrder := strings.Contains(errorBody, "most recent call last")

	pick := func(l string) (string, string, bool) {
		if m := panicLine.FindStringSubmatch(l); m != nil {
			return strings.ToLower(strings.TrimSpace(m[1])), m[2], true
		}
		if m := errorLine.FindStringSubmatch(l); m != nil {
			return m[1], m[2], true
		}
		return "", "", false
	}

	if pythonOrder {
		for i := len(lines) - 1; i >= 0; i-- {
			if c, msg, ok := pick(lines[i]); ok {
				return c, msg
			}
		}
	}
	for _, l := range lines {
		if c, msg, ok := pick(l); ok {
			return c, msg
		}
	}

	// No error class: fall back to the first non-empty line — for a threshold
	// alert, that is its own summary.
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return "", t
		}
	}
	return "", ""
}

// mask replaces every volatile substring and collapses whitespace.
func mask(s string) string {
	s = strings.TrimSpace(s)
	for _, m := range masks {
		s = m.re.ReplaceAllString(s, m.with)
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ToLower(s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// normalizeFramePath strips the reported build prefix, so the same deployment
// built in a different directory still fingerprints identically.
func normalizeFramePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, prefix := range []string{"/app/", "/srv/", "/usr/src/app/", "/home/", "/workspace/", "/build/"} {
		if i := strings.Index(p, prefix); i >= 0 {
			p = p[i+len(prefix):]
			break
		}
	}
	return strings.ToLower(strings.TrimPrefix(p, "./"))
}

// tokensFor builds the bag of words fuzzy matching compares: the masked
// signature plus the stack's file paths.
func tokensFor(text string, frames []traverse.Frame) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) < 3 || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}

	for _, t := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}) {
		add(t)
	}
	// Frames past the top still say a lot about where a failure lives.
	for i, f := range frames {
		if i >= 5 {
			break
		}
		add(normalizeFramePath(f.Path))
		add(f.Func)
	}

	sort.Strings(out) // stable order keeps stored token arrays comparable
	return out
}

// DefaultSimilarity is the Jaccard overlap two signatures must share before a
// stored fix is reused. Deliberately high: applying the wrong cached fix is far
// worse than falling through to the model.
const DefaultSimilarity = 0.75

// Similarity is the Jaccard index of two token bags; 1.0 is identical vocabulary.
func Similarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	inter := 0
	seen := make(map[string]bool, len(b))
	for _, t := range b {
		if seen[t] {
			continue
		}
		seen[t] = true
		if set[t] {
			inter++
		}
	}
	union := len(set) + len(seen) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
