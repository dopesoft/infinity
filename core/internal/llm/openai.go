package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
)

type OpenAI struct {
	client openai.Client
	model  string
}

func NewOpenAI(apiKey, model string) *OpenAI {
	if model == "" {
		model = "gpt-5"
	}
	c := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAI{client: c, model: model}
}

func (o *OpenAI) Name() string  { return "openai" }
func (o *OpenAI) Model() string { return o.model }

func (o *OpenAI) Stream(
	ctx context.Context,
	model string,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	effectiveModel := o.model
	if model != "" {
		if normalized := normalizeOpenAIModel(model); normalized != "" {
			effectiveModel = normalized
		}
		// Unknown nickname (e.g. "haiku"/"sonnet") silently falls back to
		// the configured default so an upstream bad guess can't break the
		// turn.
	}
	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	if system != "" {
		apiMessages = append(apiMessages, openai.SystemMessage(system))
	}
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			apiMessages = append(apiMessages, openai.UserMessage(m.Content))
		case RoleAssistant:
			am := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				am.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				}
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Input)
				am.ToolCalls = append(am.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID:   tc.ID,
					Type: "function",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(args),
					},
				})
			}
			apiMessages = append(apiMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &am})
		case RoleTool:
			apiMessages = append(apiMessages, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	apiTools := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		apiTools = append(apiTools, openai.ChatCompletionToolParam{
			Type: "function",
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  toFunctionParameters(t.Schema),
			},
		})
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(effectiveModel),
		Messages: apiMessages,
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}

	stream := o.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var (
		acc     openai.ChatCompletionAccumulator
		resp    Response
		streamErr error
	)

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				emit(out, StreamEvent{Kind: StreamText, TextDelta: choice.Delta.Content})
			}
		}
	}
	if err := stream.Err(); err != nil {
		streamErr = err
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
	}

	if len(acc.Choices) > 0 {
		msg := acc.Choices[0].Message
		resp.Text = msg.Content
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					return resp, fmt.Errorf("decode openai tool arguments: %w", err)
				}
			}
			call := ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input}
			resp.ToolCalls = append(resp.ToolCalls, call)
			emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &call})
		}
		resp.Usage = TokenUsage{Input: int(acc.Usage.PromptTokens), Output: int(acc.Usage.CompletionTokens)}
		resp.StopReason = string(acc.Choices[0].FinishReason)
	}

	emit(out, StreamEvent{Kind: StreamComplete, StopReason: resp.StopReason, Usage: &resp.Usage})
	return resp, streamErr
}

func toFunctionParameters(schema map[string]any) shared.FunctionParameters {
	if schema == nil {
		return shared.FunctionParameters{"type": "object"}
	}
	if !strings.Contains(fmt.Sprint(schema), "type") {
		schema["type"] = "object"
	}
	out := shared.FunctionParameters{}
	for k, v := range schema {
		out[k] = v
	}
	return out
}

// normalizeOpenAIModel maps full ids + nicknames onto canonical OpenAI
// model strings. Returns "" if the input doesn't look like something
// this provider can serve, so the caller can fall back to its own
// default. Mirrors normalizeAnthropicModel on the other side so the
// delegate tool can pass either tier shorthand or a full id without
// caring which provider is wired up.
func normalizeOpenAIModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	// Pass-through for any full id in the OpenAI namespace.
	if strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") ||
		strings.HasPrefix(m, "chatgpt-") {
		return model
	}
	// Map Anthropic tier nicknames onto the closest OpenAI tier so an
	// agent that learned "haiku for cheap" doesn't tank when the loop is
	// on OpenAI. Adjust these in lock step with the OpenAI lineup.
	switch m {
	case "haiku", "cheap", "small", "mini":
		return "gpt-5-mini"
	case "sonnet", "default", "medium":
		return "gpt-5"
	case "opus", "premium", "large":
		return "gpt-5"
	}
	return ""
}

var _ ssestream.Stream[openai.ChatCompletionChunk] // keep import for clarity
