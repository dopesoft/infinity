package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type Anthropic struct {
	client          anthropic.Client
	model           string
	thinkingBudget  int64 // 0 = disabled. ≥1024 enables extended thinking with that token budget.
}

func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Anthropic{client: c, model: model, thinkingBudget: thinkingBudgetFromEnv()}
}

// thinkingBudgetFromEnv reads ANTHROPIC_THINKING_BUDGET. Anthropic requires
// a budget ≥1024 tokens; values below that disable extended thinking entirely
// so the user doesn't accidentally pay for a feature that won't activate.
func thinkingBudgetFromEnv() int64 {
	raw := os.Getenv("ANTHROPIC_THINKING_BUDGET")
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1024 {
		return 0
	}
	return n
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.model }

// Draft is a one-shot, non-streaming completion used by helper subsystems
// (e.g. the Voyager skill extractor) that need a quick JSON-shaped reply
// without setting up the full streaming loop. Returns the concatenated text
// of the response.
func (a *Anthropic) Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error) {
	if model == "" {
		model = a.model
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return "", err
	}
	var b []byte
	for _, c := range msg.Content {
		if t, ok := c.AsAny().(anthropic.TextBlock); ok {
			b = append(b, t.Text...)
		}
	}
	return string(b), nil
}

func (a *Anthropic) Stream(
	ctx context.Context,
	model string,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	// Per-call model override (studio model chip). Falls back to the
	// boot-time default when unset. We don't validate the string here —
	// the Anthropic API surfaces a 404 / invalid-model error if the
	// client requests something unrecognized, which the WS error path
	// already plumbs back to the user.
	effectiveModel := a.model
	if model != "" {
		if normalized := normalizeAnthropicModel(model); normalized != "" {
			effectiveModel = normalized
		}
		// Unrecognized names (e.g. "gpt-5", "o3") silently fall back to
		// the boot-time default instead of erroring at the API. The agent
		// passing the wrong nickname should never tank the whole turn.
	}
	apiMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			apiMessages = append(apiMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				input, _ := json.Marshal(tc.Input)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, json.RawMessage(input), tc.Name))
			}
			if len(blocks) > 0 {
				apiMessages = append(apiMessages, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			apiMessages = append(apiMessages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	apiTools := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		raw, _ := json.Marshal(t.Schema)
		var schema anthropic.ToolInputSchemaParam
		_ = json.Unmarshal(raw, &schema)
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: schema,
			},
		})
	}

	maxTokens := int64(4096)
	if a.thinkingBudget > 0 && a.thinkingBudget+1024 > maxTokens {
		maxTokens = a.thinkingBudget + 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(effectiveModel),
		MaxTokens: maxTokens,
		Messages:  apiMessages,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}
	if a.thinkingBudget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(a.thinkingBudget)
	}

	stream := a.client.Messages.NewStreaming(ctx, params)

	var msg anthropic.Message
	var resp Response

	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
			return resp, err
		}

		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch d := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if d.Text != "" {
					emit(out, StreamEvent{Kind: StreamText, TextDelta: d.Text})
				}
			case anthropic.ThinkingDelta:
				if d.Thinking != "" {
					emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: d.Thinking})
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Text += b.Text
		case anthropic.ToolUseBlock:
			var input map[string]any
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &input); err != nil {
					return resp, fmt.Errorf("decode tool input: %w", err)
				}
			}
			tc := ToolCall{ID: b.ID, Name: b.Name, Input: input}
			resp.ToolCalls = append(resp.ToolCalls, tc)
			emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
		}
	}

	resp.Usage = TokenUsage{Input: int(msg.Usage.InputTokens), Output: int(msg.Usage.OutputTokens)}
	resp.StopReason = string(msg.StopReason)
	emit(out, StreamEvent{Kind: StreamComplete, StopReason: resp.StopReason, Usage: &resp.Usage})

	return resp, nil
}

// normalizeAnthropicModel maps known nicknames + full ids onto canonical
// Anthropic model strings. Returns "" if the input doesn't look like
// anything this provider can serve, so the caller can fall back to its
// own default. Keeps the delegate tool resilient to whatever shorthand
// the agent decides to pass on a given turn.
func normalizeAnthropicModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	// Pass-through for any full id already in the Claude namespace.
	if strings.HasPrefix(m, "claude-") {
		return model
	}
	// Tier nicknames map to the current generation. Update these in lock
	// step with the model knowledge cutoff section of CLAUDE.md.
	switch m {
	case "haiku":
		return "claude-haiku-4-5-20251001"
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-7"
	}
	return ""
}

func emit(ch chan<- StreamEvent, ev StreamEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}
