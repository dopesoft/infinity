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
	StopReason    string          `json:"stop_reason,omitempty"`
	Usage         *TokenUsage     `json:"usage,omitempty"`
	Err           string          `json:"err,omitempty"`
}

type StreamEventKind string

const (
	StreamText     StreamEventKind = "text"
	StreamThinking StreamEventKind = "thinking"
	StreamToolCall StreamEventKind = "tool_call"
	StreamComplete StreamEventKind = "complete"
	StreamError    StreamEventKind = "error"
)

type Provider interface {
	Name() string
	Model() string
	Stream(ctx context.Context, system string, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error)
}

var ErrNotImplemented = errors.New("provider not implemented")
