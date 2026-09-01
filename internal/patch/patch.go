// Package patch is the fix representation shared by the stages that produce and
// consume one: the model proposes patches, the pattern cache stores them, and
// the pull-request stage applies them.
//
// A fix is a set of exact-string replacements, not a unified diff. Diff line
// numbers drift, and a miscounted hunk header applies cleanly in the wrong
// place; an exact replacement either matches byte-for-byte or does not apply,
// which makes a bad fix a rejection instead of a corruption.
package patch

import (
	"fmt"
	"strings"
)

// Edit replaces one exact occurrence of Old with New in the file at Path.
type Edit struct {
	Path string `json:"path"`
	Old  string `json:"old_string"`
	New  string `json:"new_string"`
	Why  string `json:"explanation,omitempty"`
}

// FileChange is the result of applying every edit that targets one file.
type FileChange struct {
	Path      string `json:"path"`
	Before    string `json:"-"`
	After     string `json:"-"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Edits     int    `json:"edits"`
}

// Error describes why an edit could not be applied.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("patch: %s: %s", e.Path, e.Reason) }

// Apply applies edits to the supplied file contents, keyed by repo-relative
// path, returning one FileChange per touched file in first-edited order.
//
// Every edit must match its file exactly once. No match means the model invented
// code; several means the anchor is ambiguous. Both are refused, not guessed.
func Apply(files map[string]string, edits []Edit) ([]FileChange, error) {
	if len(edits) == 0 {
		return nil, fmt.Errorf("patch: no edits to apply")
	}

	// working accumulates edits, so several edits to one file compose.
	working := make(map[string]string, len(files))
	original := make(map[string]string, len(files))
	counts := make(map[string]int, len(files))
	var order []string

	for _, e := range edits {
		content, ok := files[e.Path]
		if !ok {
			return nil, &Error{e.Path, "file was not part of the fix context"}
		}
		if e.Old == "" {
			return nil, &Error{e.Path, "edit has an empty anchor"}
		}
		if e.Old == e.New {
			return nil, &Error{e.Path, "edit is a no-op"}
		}

		if _, seen := working[e.Path]; !seen {
			working[e.Path] = content
			original[e.Path] = content
			order = append(order, e.Path)
		}

		cur := working[e.Path]
		switch n := strings.Count(cur, e.Old); {
		case n == 0:
			return nil, &Error{e.Path, fmt.Sprintf("anchor not found in file: %q", preview(e.Old))}
		case n > 1:
			return nil, &Error{e.Path, fmt.Sprintf("anchor matches %d times, need exactly one: %q", n, preview(e.Old))}
		}

		working[e.Path] = strings.Replace(cur, e.Old, e.New, 1)
		counts[e.Path]++
	}

	changes := make([]FileChange, 0, len(order))
	for _, path := range order {
		before, after := original[path], working[path]
		adds, dels := diffStat(before, after)
		changes = append(changes, FileChange{
			Path:      path,
			Before:    before,
			After:     after,
			Additions: adds,
			Deletions: dels,
			Edits:     counts[path],
		})
	}
	return changes, nil
}

// Paths returns the distinct files an edit set touches.
func Paths(edits []Edit) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range edits {
		if e.Path != "" && !seen[e.Path] {
			seen[e.Path] = true
			out = append(out, e.Path)
		}
	}
	return out
}

// diffStat counts added and removed lines, using an LCS so lines that merely
// moved are not counted as both.
func diffStat(before, after string) (additions, deletions int) {
	a := splitLines(before)
	b := splitLines(after)

	// Trimming the common prefix and suffix keeps the LCS table proportional to
	// the change rather than to the file.
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	a, b = a[start:endA], b[start:endB]

	common := lcsLen(a, b)
	return len(b) - common, len(a) - common
}

// lcsLen computes the longest common subsequence with a rolling row, so memory
// stays O(min(len(a), len(b))).
func lcsLen(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(b) > len(a) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// preview shortens an anchor so an error names the code it looked for without
// dumping a whole function into the log.
func preview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " ⏎ "))
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
