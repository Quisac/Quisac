package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// DefaultAnthropicModel: fixes are correctness-critical and run rarely, so this
// is the strongest option rather than the cheapest.
const DefaultAnthropicModel = "claude-opus-5"

// anthropicFallbackModel serves the request if a classifier declines the primary
// model — traces from security and auth code can read as hostile out of context.
const anthropicFallbackModel = "claude-opus-4-8"

// anthropicClient proposes fixes via the Anthropic Messages API.
type anthropicClient struct {
	api    anthropic.Client
	model  string
	effort anthropic.BetaOutputConfigEffort
}

func newAnthropic(cfg Config) (*anthropicClient, error) {
	model := cfg.Model
	if model == "" {
		model = DefaultAnthropicModel
	}
	level, err := parseEffort(cfg.Effort)
	if err != nil {
		return nil, err
	}
	var effort anthropic.BetaOutputConfigEffort
	switch level {
	case effortLow:
		effort = anthropic.BetaOutputConfigEffortLow
	case effortMedium:
		effort = anthropic.BetaOutputConfigEffortMedium
	case effortHigh:
		effort = anthropic.BetaOutputConfigEffortHigh
	case effortXhigh:
		effort = anthropic.BetaOutputConfigEffortXhigh
	case effortMax:
		effort = anthropic.BetaOutputConfigEffortMax
	}
	return &anthropicClient{
		api:    anthropic.NewClient(option.WithAPIKey(cfg.APIKey)),
		model:  model,
		effort: effort,
	}, nil
}

// Enabled reports whether fixes can actually be proposed.
func (c *anthropicClient) Enabled() bool { return c != nil }

// Model returns the configured model id, for logging and display.
func (c *anthropicClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// proposeFixTool is the only shape the model may answer in. Strict validation
// means a response matches exactly or the API rejects it.
func proposeFixTool() anthropic.BetaToolUnionParam {
	tool := anthropic.BetaToolParam{
		Name:        toolName,
		Strict:      anthropic.Bool(true),
		Description: anthropic.String(toolDescription),
		InputSchema: anthropic.BetaToolInputSchemaParam{
			Properties: fixToolProperties(),
			Required:   fixToolRequired(),
			ExtraFields: map[string]any{
				"additionalProperties": false,
			},
		},
	}
	return anthropic.BetaToolUnionParam{OfTool: &tool}
}

// ProposeFix asks the model for a root cause and a minimal patch.
func (c *anthropicClient) ProposeFix(ctx context.Context, req FixRequest) (*Fix, error) {
	if c == nil {
		return nil, fmt.Errorf("llm: no API key configured")
	}
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("llm: no files in fix context")
	}

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		// Diagnosis benefits from reasoning before committing to an answer.
		Thinking: anthropic.BetaThinkingConfigParamUnion{
			OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{},
		},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: c.effort},
		Tools:        []anthropic.BetaToolUnionParam{proposeFixTool()},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(buildPrompt(req))),
		},
		// A declined request is re-served by the fallback inside the same call.
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
	}

	// A fix at high effort can take minutes; streaming avoids the non-streaming
	// response timeout.
	stream := c.api.Beta.Messages.NewStreaming(ctx, params)
	var msg anthropic.BetaMessage
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return nil, fmt.Errorf("llm: accumulate stream: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		if transientAnthropicError(err) {
			return nil, fmt.Errorf("%w: %v", ErrTransient, err)
		}
		return nil, fmt.Errorf("llm: propose fix: %w", err)
	}

	fix := &Fix{
		Model:        c.model,
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
	}

	if msg.StopReason == anthropic.BetaStopReasonRefusal {
		fix.NoFixReason = "the request was declined by a safety classifier"
		if msg.StopDetails.Explanation != "" {
			fix.NoFixReason += ": " + msg.StopDetails.Explanation
		}
		return fix, nil
	}

	if err := parseFixCall(&msg, fix); err != nil {
		return nil, err
	}
	return fix, nil
}

// transientAnthropicError reports whether a failure was the provider's, not the
// incident's: 429 and 5xx are "ask again later", a 400 or 401 is not.
func transientAnthropicError(err error) bool {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNRESET)
}

// parseFixCall pulls the propose_fix arguments out of the response, preserving
// prose returned instead of a call as the reason.
func parseFixCall(msg *anthropic.BetaMessage, fix *Fix) error {
	var prose []string
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			if t := strings.TrimSpace(b.Text); t != "" {
				prose = append(prose, t)
			}
		case anthropic.BetaToolUseBlock:
			if b.Name != toolName {
				continue
			}
			var payload fixCall
			// Never string-matched: models vary in how they escape tool arguments.
			if err := json.Unmarshal([]byte(b.JSON.Input.Raw()), &payload); err != nil {
				return fmt.Errorf("llm: decode %s input: %w", toolName, err)
			}
			payload.apply(fix)
			return nil
		}
	}

	if len(prose) > 0 {
		fix.RootCause = strings.Join(prose, "\n\n")
		fix.NoFixReason = "the model responded without proposing a structured fix"
		return nil
	}
	return fmt.Errorf("llm: response contained no %s call", toolName)
}
