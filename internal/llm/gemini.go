package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"

	"google.golang.org/genai"
)

// DefaultGeminiModel is chosen for having capacity, not for being the newest id
// the API lists: newer ids are listed for free-tier keys but answer 503
// UNAVAILABLE. Override with QUISAC_LLM_MODEL.
//
// Google trains on free-tier usage, and this package sends source code and
// production stack traces — a deployment handling other people's code wants a
// billed key, not this default.
const DefaultGeminiModel = "gemini-3.5-flash"

// Retry budget for one call. The SDK's default (5 attempts, doubling to 60s)
// took 7m32s to fail against a 503-ing model while holding a worker slot. The
// durable job queue already retries with backoff and without holding one, so the
// in-process budget stays tight and hands transient failures back quickly.
const (
	geminiRetryAttempts = 3
	geminiInitialDelay  = 2.0  // seconds
	geminiMaxDelay      = 15.0 // seconds
)

// geminiClient proposes fixes via the Gemini API.
type geminiClient struct {
	api      *genai.Client
	model    string
	thinking genai.ThinkingLevel
}

func newGemini(cfg Config) (*geminiClient, error) {
	model := cfg.Model
	if model == "" {
		model = DefaultGeminiModel
	}
	level, err := parseEffort(cfg.Effort)
	if err != nil {
		return nil, err
	}
	// Gemini exposes low/medium/high against our five steps, so the top three
	// collapse onto HIGH. Nothing maps onto MINIMAL: diagnosis is reasoning work.
	var thinking genai.ThinkingLevel
	switch level {
	case effortLow:
		thinking = genai.ThinkingLevelLow
	case effortMedium:
		thinking = genai.ThinkingLevelMedium
	default:
		thinking = genai.ThinkingLevelHigh
	}

	// Used only to resolve credentials at construction, never for a request.
	attempts := int32(geminiRetryAttempts)
	initial, max := geminiInitialDelay, geminiMaxDelay
	api, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			RetryOptions: &genai.HTTPRetryOptions{
				Attempts:     &attempts,
				InitialDelay: &initial,
				MaxDelay:     &max,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("llm: gemini client: %w", err)
	}
	return &geminiClient{api: api, model: model, thinking: thinking}, nil
}

// Enabled reports whether fixes can actually be proposed.
func (c *geminiClient) Enabled() bool { return c != nil }

// Model returns the configured model id, for logging and display.
func (c *geminiClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// geminiFixTool declares propose_fix from the same raw JSON Schema the Anthropic
// path uses, so the two cannot drift.
func geminiFixTool() *genai.Tool {
	return &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:                 toolName,
			Description:          toolDescription,
			ParametersJsonSchema: fixToolJSONSchema(),
		}},
	}
}

// ProposeFix asks the model for a root cause and a minimal patch.
func (c *geminiClient) ProposeFix(ctx context.Context, req FixRequest) (*Fix, error) {
	if c == nil {
		return nil, fmt.Errorf("llm: no API key configured")
	}
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("llm: no files in fix context")
	}

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		MaxOutputTokens: maxTokens,
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: c.thinking,
			// Never shown or stored, so shipping it back over the wire buys nothing.
			IncludeThoughts: false,
		},
		Tools: []*genai.Tool{geminiFixTool()},
		// ANY forces a propose_fix call. A decline is still expressible as a call
		// with no edits and a no_fix_reason, so this costs no honesty.
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{toolName},
			},
		},
	}

	resp, err := c.api.Models.GenerateContent(ctx, c.model, genai.Text(buildPrompt(req)), cfg)
	if err != nil {
		if transientGeminiError(err) {
			return nil, fmt.Errorf("%w: %v", ErrTransient, err)
		}
		return nil, fmt.Errorf("llm: propose fix: %w", err)
	}

	fix := &Fix{Model: c.model}
	if u := resp.UsageMetadata; u != nil {
		fix.InputTokens = int(u.PromptTokenCount)
		// Billed as output but reported separately; without this a high-thinking
		// fix looks nearly free in the logs.
		fix.OutputTokens = int(u.CandidatesTokenCount + u.ThoughtsTokenCount)
	}

	// A prompt blocked before generation produces no candidates.
	if len(resp.Candidates) == 0 {
		fix.NoFixReason = "the request was declined before generation"
		if fb := resp.PromptFeedback; fb != nil && fb.BlockReasonMessage != "" {
			fix.NoFixReason += ": " + fb.BlockReasonMessage
		}
		return fix, nil
	}

	// A safety stop is a decline, not a failure: the incident still reaches a
	// human with whatever analysis exists.
	switch reason := resp.Candidates[0].FinishReason; reason {
	case genai.FinishReasonSafety,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonBlocklist,
		genai.FinishReasonSPII,
		genai.FinishReasonRecitation:
		fix.NoFixReason = fmt.Sprintf("the request was declined by a safety classifier (%s)", reason)
		if msg := resp.Candidates[0].FinishMessage; msg != "" {
			fix.NoFixReason += ": " + msg
		}
		return fix, nil
	case genai.FinishReasonMalformedFunctionCall:
		return nil, fmt.Errorf("llm: model emitted a malformed %s call", toolName)
	case genai.FinishReasonMaxTokens:
		// The call is truncated, so its edits are unusable.
		return nil, fmt.Errorf("llm: response hit the %d-token cap before completing", maxTokens)
	}

	if err := parseGeminiFixCall(resp, fix); err != nil {
		return nil, err
	}
	return fix, nil
}

// transientGeminiError reports whether a failure was the provider's, not the
// incident's: 429 and 5xx are "ask again later", a 400 or 403 is a defect.
func transientGeminiError(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}
	// A reset or timeout never reaches the API error path, and is transient too.
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNRESET)
}

// parseGeminiFixCall pulls the propose_fix arguments out of the response,
// preserving prose returned instead of a call as the reason.
func parseGeminiFixCall(resp *genai.GenerateContentResponse, fix *Fix) error {
	for _, call := range resp.FunctionCalls() {
		if call.Name != toolName {
			continue
		}
		// Args arrives decoded; re-marshal once to reuse the shared struct tags.
		raw, err := json.Marshal(call.Args)
		if err != nil {
			return fmt.Errorf("llm: re-encode %s args: %w", toolName, err)
		}
		var payload fixCall
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("llm: decode %s input: %w", toolName, err)
		}
		payload.apply(fix)
		return nil
	}

	if prose := strings.TrimSpace(resp.Text()); prose != "" {
		fix.RootCause = prose
		fix.NoFixReason = "the model responded without proposing a structured fix"
		return nil
	}
	return fmt.Errorf("llm: response contained no %s call", toolName)
}
