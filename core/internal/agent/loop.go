// Package agent implements Infinity's intentionally-small agent loop.
// Inspired by nanobot's design: receive prompt → build context → call LLM →
// dispatch tools → repeat until the model returns text.
//
// The memory subsystem attaches via MemoryProvider (search) and the hooks
// pipeline via HookEmitter (capture).
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
)

// SkillMatcher is implemented by skills.Registry. Decoupled to keep the agent
// package free of skill-package dependencies.
type SkillMatcher interface {
	MatchAndPrefix(message string, limit int) string
}

// defaultSystemPrompt is the fallback when no soul has been loaded.
// In practice the soul package always supplies one (embedded soul.md);
// this exists only so a misconfigured Loop still has a sane persona.
const defaultSystemPrompt = `You are Jarvis, the boss's personal AI agent running inside Infinity.

You have access to tools. When a tool call moves the work forward, make it. Don't ask permission for routine work and don't narrate the call afterwards — integrate the result into your reply.

Be concise. Address the user as "boss". Cite memory sources when you rely on them.`

type Session struct {
	ID        string
	Project   string
	StartedAt time.Time
	Messages  []llm.Message
	mu        sync.Mutex
}

func (s *Session) Append(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now().UTC()
	}
	s.Messages = append(s.Messages, m)
}

func (s *Session) Snapshot() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.Message, len(s.Messages))
	copy(out, s.Messages)
	return out
}

// MemoryProvider lets memory inject relevant retrievals without coupling.
type MemoryProvider interface {
	BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error)
}

// HookEmitter is implemented by hooks.Pipeline. Decoupled here.
type HookEmitter interface {
	Emit(name string, sessionID, project, text string, payload map[string]any)
}

type Loop struct {
	llmProvider llm.Provider
	tools       *tools.Registry
	memory      MemoryProvider
	hooks       HookEmitter
	skills      SkillMatcher

	mu       sync.Mutex
	sessions map[string]*Session

	systemPrompt      string
	maxToolIterations int
}

type Config struct {
	LLM               llm.Provider
	Tools             *tools.Registry
	Memory            MemoryProvider
	Hooks             HookEmitter
	Skills            SkillMatcher
	SystemPrompt      string
	MaxToolIterations int
}

func New(cfg Config) *Loop {
	if cfg.MaxToolIterations <= 0 {
		cfg.MaxToolIterations = 8
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if cfg.Tools == nil {
		cfg.Tools = tools.NewRegistry()
	}
	return &Loop{
		llmProvider:       cfg.LLM,
		tools:             cfg.Tools,
		memory:            cfg.Memory,
		hooks:             cfg.Hooks,
		skills:            cfg.Skills,
		systemPrompt:      cfg.SystemPrompt,
		maxToolIterations: cfg.MaxToolIterations,
		sessions:          make(map[string]*Session),
	}
}

func (l *Loop) Provider() llm.Provider { return l.llmProvider }
func (l *Loop) Tools() *tools.Registry { return l.tools }

func (l *Loop) GetOrCreateSession(id string) *Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		id = uuid.NewString()
	}
	s, ok := l.sessions[id]
	if !ok {
		s = &Session{ID: id, StartedAt: time.Now().UTC()}
		l.sessions[id] = s
		l.fireHook("SessionStart", s.ID, s.Project, "session started", map[string]any{"id": s.ID})
	}
	return s
}

func (l *Loop) ClearSession(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.sessions[id]; ok {
		l.fireHook("SessionEnd", id, "", "session cleared", nil)
	}
	delete(l.sessions, id)
}

func (l *Loop) Sessions() []*Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*Session, 0, len(l.sessions))
	for _, s := range l.sessions {
		out = append(out, s)
	}
	return out
}

// RunEvent is what we surface to transports (WebSocket/etc).
type RunEvent struct {
	Kind          EventKind       `json:"kind"`
	SessionID     string          `json:"session_id"`
	TextDelta     string          `json:"text_delta,omitempty"`
	ThinkingDelta string          `json:"thinking_delta,omitempty"`
	ToolCall      *ToolEvent      `json:"tool_call,omitempty"`
	ToolResult    *ToolEvent      `json:"tool_result,omitempty"`
	Usage         *llm.TokenUsage `json:"usage,omitempty"`
	Error         string          `json:"error,omitempty"`
	StopReason    string          `json:"stop_reason,omitempty"`
}

type ToolEvent struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input,omitempty"`
	Output    string         `json:"output,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	EndedAt   time.Time      `json:"ended_at,omitempty"`
}

type EventKind string

const (
	EventDelta      EventKind = "delta"
	EventThinking   EventKind = "thinking"
	EventToolCall   EventKind = "tool_call"
	EventToolResult EventKind = "tool_result"
	EventComplete   EventKind = "complete"
	EventError      EventKind = "error"
)

func (l *Loop) Run(ctx context.Context, sessionID, userMsg string, out chan<- RunEvent) error {
	if l.llmProvider == nil {
		return errors.New("agent loop has no LLM provider configured")
	}

	s := l.GetOrCreateSession(sessionID)
	s.Append(llm.Message{Role: llm.RoleUser, Content: userMsg})

	l.fireHook("UserPromptSubmit", s.ID, s.Project, userMsg, nil)

	systemPrompt := l.systemPrompt
	if l.memory != nil {
		prefix, err := l.memory.BuildSystemPrefix(ctx, s.ID, userMsg)
		if err == nil && prefix != "" {
			systemPrompt = prefix + "\n\n" + systemPrompt
		}
	}
	if l.skills != nil {
		if skillsPrefix := l.skills.MatchAndPrefix(userMsg, 5); skillsPrefix != "" {
			systemPrompt = skillsPrefix + "\n\n" + systemPrompt
		}
	}

	for iter := 0; iter < l.maxToolIterations; iter++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		llmEvents := make(chan llm.StreamEvent, 64)
		var resp llm.Response
		var streamErr error
		streamDone := make(chan struct{})

		go func() {
			defer close(streamDone)
			resp, streamErr = l.llmProvider.Stream(ctx, systemPrompt, s.Snapshot(), l.tools.Definitions(), llmEvents)
			close(llmEvents)
		}()

		for ev := range llmEvents {
			switch ev.Kind {
			case llm.StreamText:
				emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: ev.TextDelta})
			case llm.StreamThinking:
				emit(out, RunEvent{Kind: EventThinking, SessionID: s.ID, ThinkingDelta: ev.ThinkingDelta})
			case llm.StreamError:
				emit(out, RunEvent{Kind: EventError, SessionID: s.ID, Error: ev.Err})
			}
		}

		<-streamDone

		if streamErr != nil {
			emit(out, RunEvent{Kind: EventError, SessionID: s.ID, Error: streamErr.Error()})
			return streamErr
		}

		if len(resp.ToolCalls) == 0 {
			s.Append(llm.Message{Role: llm.RoleAssistant, Content: resp.Text})
			emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, Usage: &resp.Usage, StopReason: resp.StopReason})
			l.fireHook("TaskCompleted", s.ID, s.Project, resp.Text, map[string]any{
				"input_tokens":  resp.Usage.Input,
				"output_tokens": resp.Usage.Output,
			})
			return nil
		}

		s.Append(llm.Message{Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			startedAt := time.Now().UTC()
			emit(out, RunEvent{
				Kind:      EventToolCall,
				SessionID: s.ID,
				ToolCall: &ToolEvent{
					ID:        tc.ID,
					Name:      tc.Name,
					Input:     tc.Input,
					StartedAt: startedAt,
				},
			})
			l.fireHook("PreToolUse", s.ID, s.Project, tc.Name, map[string]any{"name": tc.Name, "input": tc.Input})

			output, execErr := l.tools.Execute(ctx, tc)
			endedAt := time.Now().UTC()

			isErr := execErr != nil
			if isErr {
				output = fmt.Sprintf("ERROR: %v", execErr)
			}

			emit(out, RunEvent{
				Kind:      EventToolResult,
				SessionID: s.ID,
				ToolResult: &ToolEvent{
					ID:        tc.ID,
					Name:      tc.Name,
					Output:    output,
					IsError:   isErr,
					StartedAt: startedAt,
					EndedAt:   endedAt,
				},
			})

			hookName := "PostToolUse"
			if isErr {
				hookName = "PostToolUseFailure"
			}
			l.fireHook(hookName, s.ID, s.Project, tc.Name+": "+output, map[string]any{
				"name":   tc.Name,
				"input":  tc.Input,
				"output": output,
			})

			s.Append(llm.Message{
				Role:       llm.RoleTool,
				Content:    output,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
			})
		}
	}

	err := errors.New("agent loop exceeded maximum tool iterations")
	emit(out, RunEvent{Kind: EventError, SessionID: s.ID, Error: err.Error()})
	return err
}

func (l *Loop) fireHook(name, sessionID, project, text string, payload map[string]any) {
	if l.hooks == nil {
		return
	}
	l.hooks.Emit(name, sessionID, project, text, payload)
}

func emit(ch chan<- RunEvent, ev RunEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}
