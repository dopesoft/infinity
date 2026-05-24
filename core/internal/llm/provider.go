package llm

import (
	"context"
	"errors"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Timestamp  time.Time      `json:"timestamp,omitempty"`
	Meta       map[string]any `json:"-"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"input_schema"`
}

type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type TokenUsage struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
}

type Response struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls"`
	Usage     TokenUsage `json:"usage"`
	StopReason string    `json:"stop_reason"`
}

type StreamEvent struct {
	Kind          StreamEventKind `json:"kind"`
	TextDelta     string          `json:"text_delta,omitempty"`
	ThinkingDelta string          `json:"thinking_delta,omitempty"`
	ToolCall      *ToolCall       `json:"tool_call,omitempty"`
	// Tool-input streaming: a StreamToolInputDelta carries the model writing a
	// tool call's arguments live, BEFORE the tool runs and before the final
	// StreamToolCall. ToolCallID/ToolName identify which call (set as soon as
	// the provider knows them); InputDelta is the raw partial-JSON argument
	// chunk. Model-agnostic: providers that can stream tool args emit these;
	// ones that can't simply fall back to the complete StreamToolCall, so
	// consumers treat deltas as a best-effort live preview that the final tool
	// call reconciles authoritatively.
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	InputDelta string      `json:"input_delta,omitempty"`
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *TokenUsage `json:"usage,omitempty"`
	Err        string      `json:"err,omitempty"`
}

type StreamEventKind string

const (
	StreamText           StreamEventKind = "text"
	StreamThinking       StreamEventKind = "thinking"
	StreamToolCall       StreamEventKind = "tool_call"
	StreamToolInputDelta StreamEventKind = "tool_input_delta"
	StreamComplete       StreamEventKind = "complete"
	StreamError          StreamEventKind = "error"
)

type Provider interface {
	Name() string
	// Model returns the provider's default model id (the one configured
	// at boot via LLM_MODEL). Callers that want a per-call override pass
	// it as the `model` arg to Stream.
	Model() string
	// Stream runs one turn. `model` is an optional per-call override -
	// empty string falls back to the provider's default. This is how the
	// studio's model chip switches between Sonnet / Opus / Haiku on a
	// per-turn basis without restarting Core.
	Stream(ctx context.Context, model, system string, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error)
}

var ErrNotImplemented = errors.New("provider not implemented")
