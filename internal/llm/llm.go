// Package llm proposes a patch for an incident the pattern cache has never
// seen. By the time a request reaches here the graph has narrowed the repository
// to a handful of ranked symbols and the files behind them, so the prompt stays
// small.
//
// The model never edits anything. It returns exact-string replacements, which
// the patch package applies only if every anchor matches its file exactly once.
//
// This file holds everything vendor-neutral — types, tool schema, system prompt,
// prompt assembly — and each provider implements Fixer over them.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"quisac/internal/patch"
	"quisac/internal/traverse"
)

// maxTokens leaves headroom for a fix and its rationale without inviting a rewrite.
const maxTokens = 16000

// Provider selects which vendor proposes fixes.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
)

// DefaultProvider keeps deployments that only set an API key working unchanged.
const DefaultProvider = ProviderAnthropic

// ErrTransient marks a failure that says nothing about the incident — overloaded,
// rate-limited, briefly unreachable. The pipeline must hand these back to the job
// queue rather than record them as a resolution outcome.
var ErrTransient = errors.New("llm: transient provider failure")

// Fixer proposes patches. Package resolve deliberately redeclares it, so it
// depends on the behaviour rather than on this package's clients.
type Fixer interface {
	Enabled() bool
	Model() string
	ProposeFix(ctx context.Context, req FixRequest) (*Fix, error)
}

// Config configures the client. Only APIKey is required.
type Config struct {
	Provider string // anthropic | gemini; defaults to DefaultProvider
	APIKey   string
	Model    string // defaults to the provider's default model
	Effort   string // low | medium | high | xhigh | max; defaults to xhigh
}

// New builds the configured provider's client, returning (nil, nil) when no API
// key is set: "no LLM" is a supported deployment, where novel incidents stop at a
// ranked candidate list. That nil is an untyped nil interface, not a typed nil
// pointer, so the caller's `fixer == nil` check behaves as it reads.
func New(cfg Config) (Fixer, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, nil
	}

	provider := Provider(strings.ToLower(strings.TrimSpace(cfg.Provider)))
	if provider == "" {
		provider = DefaultProvider
	}
	switch provider {
	case ProviderAnthropic:
		return newAnthropic(cfg)
	case ProviderGemini:
		return newGemini(cfg)
	default:
		return nil, fmt.Errorf("llm: unknown provider %q (want %q or %q)",
			cfg.Provider, ProviderAnthropic, ProviderGemini)
	}
}

// effortLevel is the vendor-neutral reasoning budget.
type effortLevel int

const (
	effortLow effortLevel = iota
	effortMedium
	effortHigh
	effortXhigh
	effortMax
)

// parseEffort validates once, so both providers reject the same typos alike.
func parseEffort(s string) (effortLevel, error) {
	if strings.TrimSpace(s) == "" {
		return effortXhigh, nil
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return effortLow, nil
	case "medium":
		return effortMedium, nil
	case "high":
		return effortHigh, nil
	case "xhigh":
		return effortXhigh, nil
	case "max":
		return effortMax, nil
	default:
		return 0, fmt.Errorf("llm: unknown effort %q", s)
	}
}

// FixRequest is everything the model is shown. Files is the set the graph
// selected, keyed by repo-relative path; nothing else from the repo is sent.
type FixRequest struct {
	Repo     string
	Incident traverse.Incident
	Result   *traverse.Result
	Files    map[string]string

	// Skipped names files the graph selected but that were too large to include.
	Skipped []string
}

// Fix is a proposed change, or a documented refusal to propose one.
type Fix struct {
	RootCause  string       `json:"root_cause"`
	Summary    string       `json:"summary"`
	Confidence float64      `json:"confidence"`
	Edits      []patch.Edit `json:"edits"`

	// NoFixReason is set when the model declined — a valid outcome: the incident
	// reaches a human with an analysis instead of a plausible wrong patch.
	NoFixReason string `json:"no_fix_reason"`

	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Model        string `json:"model"`
}

// Proposed reports whether the model actually returned a change to apply.
func (f *Fix) Proposed() bool { return f != nil && len(f.Edits) > 0 }

// fixCall is the decoded tool payload, shared by both providers.
type fixCall struct {
	RootCause   string       `json:"root_cause"`
	Summary     string       `json:"summary"`
	Confidence  float64      `json:"confidence"`
	Edits       []patch.Edit `json:"edits"`
	NoFixReason string       `json:"no_fix_reason"`
}

// apply copies a decoded call onto the Fix being built.
func (fc fixCall) apply(fix *Fix) {
	fix.RootCause = fc.RootCause
	fix.Summary = fc.Summary
	fix.Confidence = fc.Confidence
	fix.Edits = fc.Edits
	fix.NoFixReason = fc.NoFixReason
}

// toolName is the only function the model may call, on every provider.
const toolName = "propose_fix"

// toolDescription is shown to the model alongside the schema.
const toolDescription = "Report the root cause of the incident and, if you are confident, " +
	"the minimal code change that fixes it."

// fixToolProperties is plain JSON Schema rather than any vendor's type, so both
// providers share one definition.
func fixToolProperties() map[string]any {
	editSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"path", "old_string", "new_string", "explanation"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Repo-relative path of the file to change. Must be one of the files provided.",
			},
			"old_string": map[string]any{
				"type": "string",
				"description": "The exact existing code to replace, copied byte-for-byte from the file including indentation. " +
					"It must appear EXACTLY ONCE in the file — include enough surrounding lines to make it unique.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The replacement code, in the same style and indentation as the surrounding file.",
			},
			"explanation": map[string]any{
				"type":        "string",
				"description": "One sentence on what this specific edit changes and why.",
			},
		},
	}

	return map[string]any{
		"root_cause": map[string]any{
			"type": "string",
			"description": "Two or three sentences naming the specific defect and the mechanism by which it " +
				"produced this alert. Reference concrete symbols and files.",
		},
		"summary": map[string]any{
			"type":        "string",
			"description": "One line describing the fix, phrased as a commit subject (e.g. \"guard against nil customer on refund path\").",
		},
		"confidence": map[string]any{
			"type":        "number",
			"description": "0-1. How confident you are that this change fixes the incident. Below 0.5, propose no edits.",
		},
		"edits": map[string]any{
			"type":        "array",
			"items":       editSchema,
			"description": "The minimal set of edits. Empty if you are not confident enough to propose a change.",
		},
		"no_fix_reason": map[string]any{
			"type": "string",
			"description": "Empty when you propose edits. Otherwise, what you would need to be confident — " +
				"a missing file, an unclear invariant, a cause outside the application code.",
		},
	}
}

// fixToolRequired lists every property, so a response is complete or rejected.
func fixToolRequired() []string {
	return []string{"root_cause", "summary", "confidence", "edits", "no_fix_reason"}
}

// fixToolJSONSchema is the whole parameter object, for providers that take a
// complete JSON Schema rather than its pieces.
func fixToolJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           fixToolProperties(),
		"required":             fixToolRequired(),
	}
}

// systemPrompt is stable across requests, so it is cached where the provider
// supports it; per-incident content lives in the user turn, after the breakpoint.
const systemPrompt = `You are the fix engine of an automated incident-response system. An alert has fired in production, and a code dependency graph has already traced it to a small set of files. Your job is to name the root cause and, when you are confident, write the minimal change that fixes it.

The change you propose will be opened as a pull request for a human to review and merge. Engineers will read it under time pressure during an active incident.

How to work:
- Start from the evidence. The stack trace says where the failure happened; the ranked candidates say what reaches that code and what changed recently. Reason about the actual control flow, not the plausible-sounding one.
- Fix the cause, not the symptom. Swallowing an exception, adding a broad try/except, or widening a timeout to hide a bug are not fixes. If the real fix is out of reach with the files you have, say so.
- Keep the change minimal and local. Do not refactor, rename, reformat, reorder imports, or "improve" code you were not asked about. A reviewer must be able to see the whole fix at a glance.
- Match the file. Follow its existing style, naming, error-handling idiom, and indentation exactly. Your edit should be indistinguishable from code the team wrote.
- Add no dependencies. Do not introduce a new import unless the module is already used elsewhere in the same file.
- Do not touch tests, configuration, or generated files unless the defect is genuinely there.

Writing edits:
- Each edit replaces one exact string. Copy old_string byte-for-byte from the file, including leading whitespace.
- old_string must appear EXACTLY ONCE in that file. If the line you want is not unique, include the lines above and below it until it is.
- Never use a placeholder, an ellipsis, or a comment standing in for code you did not write out.

When not to propose a fix:
- The evidence points outside the provided files (infrastructure, a dependency, a database, an upstream service).
- Several plausible causes fit and you cannot distinguish them from what you were given.
- The correct fix requires knowledge of intent that the code does not express.
In those cases return your root-cause analysis with no edits and a specific no_fix_reason. A precise "here is the cause, here is what I would need" is far more valuable to an on-call engineer than a confident guess that has to be reverted.

Respond by calling propose_fix exactly once.`

// topCandidates bounds the ranking shown; past a handful the scores flatten out.
const topCandidates = 12

// buildPrompt renders the incident, the graph's findings, and the file contents
// into the single user turn.
func buildPrompt(req FixRequest) string {
	var b strings.Builder

	b.WriteString("# Incident\n\n")
	fmt.Fprintf(&b, "Repository: %s\n", req.Repo)
	fmt.Fprintf(&b, "Service: %s\n", req.Incident.ServiceName)
	fmt.Fprintf(&b, "Title: %s\n", req.Incident.Title)
	if req.Incident.ErrorBody != "" {
		b.WriteString("\n## Alert payload\n\n```\n")
		b.WriteString(req.Incident.ErrorBody)
		b.WriteString("\n```\n")
	}

	if req.Result != nil {
		writeGraphFindings(&b, req.Result)
	}

	b.WriteString("\n# Files\n\n")
	b.WriteString("These are the complete current contents of the files the graph selected. ")
	b.WriteString("They are the only files you may edit.\n")
	if len(req.Skipped) > 0 {
		fmt.Fprintf(&b, "\nNot included (too large to send): %s. "+
			"Take them into account when judging your confidence.\n", strings.Join(req.Skipped, ", "))
	}

	for _, path := range sortedKeys(req.Files) {
		fmt.Fprintf(&b, "\n## %s\n\n```%s\n%s\n```\n", path, langFor(path), req.Files[path])
	}

	b.WriteString("\n# Task\n\n")
	b.WriteString("Determine the root cause of this incident and propose the minimal fix. ")
	b.WriteString("Call propose_fix exactly once.\n")

	return b.String()
}

// writeGraphFindings renders what the traversal established, so the model starts
// from the graph's evidence instead of re-deriving it.
func writeGraphFindings(b *strings.Builder, res *traverse.Result) {
	if len(res.Frames) > 0 {
		b.WriteString("\n## Stack trace, resolved against the index\n\n")
		b.WriteString("Innermost frame first.\n\n")
		for i, f := range res.Frames {
			if i >= 10 {
				break
			}
			fmt.Fprintf(b, "%d. %s", i, f.Path)
			if f.Line > 0 {
				fmt.Fprintf(b, ":%d", f.Line)
			}
			if f.Func != "" {
				fmt.Fprintf(b, " in %s", f.Func)
			}
			b.WriteString("\n")
		}
	}

	if res.EntryPoint != nil {
		fmt.Fprintf(b, "\n## Entry point\n\n%s `%s`\n", res.EntryPoint.Type, res.EntryPoint.Name)
	}

	if len(res.Candidates) == 0 {
		return
	}
	b.WriteString("\n## Ranked root-cause candidates\n\n")
	b.WriteString("Produced by walking backward through the dependency graph from the entry point. ")
	b.WriteString("Scores combine structural proximity, stack position, change recency, past fault history, and blast radius. ")
	b.WriteString("Treat this as evidence to weigh, not a verdict.\n\n")

	for i, c := range res.Candidates {
		if i >= topCandidates {
			break
		}
		fmt.Fprintf(b, "%d. `%s` (%s) in %s — score %.2f, %d hop(s) back",
			i+1, c.Name, c.Type, orDash(c.FilePath), c.Score, c.Depth)
		if len(c.Reasons) > 0 {
			fmt.Fprintf(b, "\n   why: %s", strings.Join(c.Reasons, "; "))
		}
		b.WriteString("\n")
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// langFor returns the fence language for a path.
func langFor(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	switch strings.ToLower(path[i+1:]) {
	case "py":
		return "python"
	case "js", "jsx", "mjs", "cjs":
		return "javascript"
	case "ts", "tsx":
		return "typescript"
	case "go":
		return "go"
	case "java":
		return "java"
	case "rb":
		return "ruby"
	case "rs":
		return "rust"
	case "kt":
		return "kotlin"
	case "cs":
		return "csharp"
	case "php":
		return "php"
	case "yml", "yaml":
		return "yaml"
	case "json":
		return "json"
	default:
		return ""
	}
}
