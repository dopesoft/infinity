package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type Anthropic struct {
	client anthropic.Client
	model  string
}

func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Anthropic{client: c, model: model}
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.model }

func (a *Anthropic) Stream(
	ctx context.Context,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
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

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		Messages:  apiMessages,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
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
			if d, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && d.Text != "" {
				emit(out, StreamEvent{Kind: StreamText, TextDelta: d.Text})
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

func emit(ch chan<- StreamEvent, ev StreamEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}
