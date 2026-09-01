package resolve

import (
	"fmt"
	"strings"

	"quisac/internal/db"
	"quisac/internal/llm"
	"quisac/internal/patterns"
	"quisac/internal/traverse"
)

// The PR body is read mid-incident, so it leads with the diagnosis, shows the
// evidence behind it, and is explicit about what was and was not verified.

// evidenceCandidates bounds how much of the ranking the body reproduces: enough
// to show the reasoning, not so much that it buries the diff.
const evidenceCandidates = 5

// modelPRBody renders the body for a fix the model proposed.
func modelPRBody(inc db.Incident, result *traverse.Result, fix *llm.Fix) string {
	var b strings.Builder

	writeIncidentHeader(&b, inc)

	b.WriteString("## Root cause\n\n")
	b.WriteString(orPlaceholder(fix.RootCause, "_Not determined._"))
	b.WriteString("\n\n")

	writeEvidence(&b, result)
	writeEdits(&b, fix)

	b.WriteString("## How this was produced\n\n")
	fmt.Fprintf(&b, "The dependency graph narrowed %s to %s, which were the only files sent to the model (`%s`).\n",
		result.Repo, plural(len(result.TopPaths(3)), "file"), fix.Model)
	fmt.Fprintf(&b, "Confidence: **%.0f%%**. Token spend: %d in / %d out.\n\n",
		fix.Confidence*100, fix.InputTokens, fix.OutputTokens)

	writeReviewNotice(&b)
	return b.String()
}

// patternPRBody renders the body for a fix replayed from the pattern cache.
func patternPRBody(p db.Pattern, similarity float64, sig patterns.Signature) string {
	var b strings.Builder

	b.WriteString("## Known incident — fix replayed from cache\n\n")
	b.WriteString("This error matches one Quisac has fixed before, so the previous fix was reapplied. ")
	b.WriteString("No graph traversal and no model call were needed.\n\n")

	b.WriteString("## Root cause\n\n")
	b.WriteString(orPlaceholder(p.RootCause, "_Recorded from a previous incident._"))
	b.WriteString("\n\n")

	b.WriteString("## Match\n\n")
	if similarity >= 1.0 {
		b.WriteString("- Exact match on the error fingerprint\n")
	} else {
		fmt.Fprintf(&b, "- Fuzzy match, %.0f%% signature overlap\n", similarity*100)
	}
	fmt.Fprintf(&b, "- Seen %s; this fix has been applied %s before\n",
		plural(p.TimesSeen, "time"), plural(p.TimesApplied, "time"))
	fmt.Fprintf(&b, "- Signature: `%s`\n\n", truncate(sig.Text, 200))

	b.WriteString("**Token spend: 0.**\n\n")

	b.WriteString("Because the cached edits are matched against the current source byte-for-byte, ")
	b.WriteString("this fix only applies if the code has not moved on since it was recorded.\n\n")

	writeReviewNotice(&b)
	return b.String()
}

// writeIncidentHeader states which alert this pull request answers.
func writeIncidentHeader(b *strings.Builder, inc db.Incident) {
	b.WriteString("## Incident\n\n")
	fmt.Fprintf(b, "**%s**\n\n", orPlaceholder(inc.Title, inc.ID))
	fmt.Fprintf(b, "- Service: `%s`\n", inc.ServiceName)
	fmt.Fprintf(b, "- PagerDuty incident: `%s`", inc.ID)
	if inc.PDNumber > 0 {
		fmt.Fprintf(b, " (#%d)", inc.PDNumber)
	}
	b.WriteString("\n")
	if inc.Urgency != "" {
		fmt.Fprintf(b, "- Urgency: %s\n", inc.Urgency)
	}
	b.WriteString("\n")
}

// writeEvidence reproduces what the graph found, so a reviewer can check the
// reasoning and not only the diff.
func writeEvidence(b *strings.Builder, result *traverse.Result) {
	if result == nil {
		return
	}

	if len(result.Frames) > 0 {
		b.WriteString("## Stack trace\n\n")
		b.WriteString("Resolved against the indexed source, innermost frame first.\n\n")
		for i, f := range result.Frames {
			if i >= evidenceCandidates {
				break
			}
			fmt.Fprintf(b, "%d. `%s`", i, f.Path)
			if f.Line > 0 {
				fmt.Fprintf(b, ":%d", f.Line)
			}
			if f.Func != "" {
				fmt.Fprintf(b, " in `%s`", f.Func)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(result.Candidates) == 0 {
		return
	}
	b.WriteString("## Graph evidence\n\n")
	b.WriteString("Ranked by walking the dependency graph back from the failure. ")
	b.WriteString("Scores combine structural proximity, stack position, change recency, past fault history, and blast radius.\n\n")
	b.WriteString("| Symbol | File | Score | Why |\n|---|---|---|---|\n")
	for i, c := range result.Candidates {
		if i >= evidenceCandidates {
			break
		}
		fmt.Fprintf(b, "| `%s` (%s) | `%s` | %.2f | %s |\n",
			c.Name, c.Type, orPlaceholder(c.FilePath, "—"), c.Score,
			orPlaceholder(strings.Join(c.Reasons, "; "), "—"))
	}
	b.WriteString("\n")
}

// writeEdits explains each edit in the diff in the model's own words.
func writeEdits(b *strings.Builder, fix *llm.Fix) {
	if len(fix.Edits) == 0 {
		return
	}
	b.WriteString("## Changes\n\n")
	for _, e := range fix.Edits {
		fmt.Fprintf(b, "- `%s` — %s\n", e.Path, orPlaceholder(e.Why, "no explanation given"))
	}
	b.WriteString("\n")
}

// writeReviewNotice must never be softened: this is a machine-authored change to
// production code, and the reviewer owns it.
func writeReviewNotice(b *strings.Builder) {
	b.WriteString("---\n\n")
	b.WriteString("Opened automatically by Quisac in response to a live alert. ")
	b.WriteString("The change has been checked to apply cleanly to the current source, ")
	b.WriteString("but it has **not** been tested or run. Review it as you would any other pull request.\n")
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
