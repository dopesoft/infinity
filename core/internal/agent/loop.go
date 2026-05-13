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
	"strings"
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

// SessionNamer is the optional Haiku-driven auto-namer. The loop notifies it
// after the first complete assistant turn in a session; the namer decides
// (cheap DB check) whether the row needs a name and fires Haiku async.
// Implementation lives in core/internal/sessions to keep the agent package
// free of llm/pgx dependencies for that subsystem.
type SessionNamer interface {
	MaybeName(sessionID, userMsg, assistantMsg string)
}

type Loop struct {
	// providerMu guards llmProvider for hot-swap from Settings PUTs. We
	// take a Read-lock on every Stream call to grab the current provider,
	// then drop the lock before doing I/O — keeps the swap path cheap and
	// concurrent turns safe.
	providerMu  sync.RWMutex
	llmProvider llm.Provider

	tools  *tools.Registry
	memory MemoryProvider
	hooks  HookEmitter
	skills SkillMatcher
	gate   ToolGate
	namer  SessionNamer

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
	Gate              ToolGate
	Namer             SessionNamer
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
	if cfg.Gate == nil {
		cfg.Gate = AllowAll{}
	}
	return &Loop{
		llmProvider:       cfg.LLM,
		tools:             cfg.Tools,
		memory:            cfg.Memory,
		hooks:             cfg.Hooks,
		skills:            cfg.Skills,
		gate:              cfg.Gate,
		namer:             cfg.Namer,
		systemPrompt:      cfg.SystemPrompt,
		maxToolIterations: cfg.MaxToolIterations,
		sessions:          make(map[string]*Session),
	}
}

func (l *Loop) Provider() llm.Provider {
	l.providerMu.RLock()
	defer l.providerMu.RUnlock()
	return l.llmProvider
}

// SetProvider swaps the active LLM provider at runtime. Used by the
// Settings PUT to flip anthropic ↔ openai_oauth ↔ google without a
// process restart. Concurrent Stream calls hold a Read-lock so they
// always see a consistent provider for the duration of the snapshot.
func (l *Loop) SetProvider(p llm.Provider) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.llmProvider = p
}

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
	// Set on tool_call events when the gate parked the call on a Trust
	// contract. Studio uses these to render inline Approve/Deny buttons
	// in the same tool card — no tab-switch required.
	AwaitingApproval bool   `json:"awaiting_approval,omitempty"`
	ContractID       string `json:"contract_id,omitempty"`
	Preview          string `json:"preview,omitempty"`
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

// Run drives one turn of the agent loop. steerCh is optional — when non-nil,
// the loop drains it at each iteration boundary (before the next LLM call)
// and appends each drained string as a fresh user message. This is what
// powers mid-turn steering from the Studio composer: a user can keep typing
// while the agent is mid-stream, and their input lands on the conversation
// before the next reasoning step. Pass nil from contexts where steering
// doesn't apply (cron, sentinels).
//
// On ctx.Done() the loop treats cancellation as a user-requested interrupt:
// whatever partial assistant text already streamed is persisted (so reload
// shows it), a TaskCompleted hook fires with {interrupted: true}, and the
// loop returns nil with a Complete event tagged stop_reason="interrupted".
// Real provider errors continue to surface as EventError + a returned error.
func (l *Loop) Run(ctx context.Context, sessionID, userMsg, model string, steerCh <-chan string, out chan<- RunEvent) error {
	if l.Provider() == nil {
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
			emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, StopReason: "interrupted"})
			return nil
		default:
		}

		// Drain steered messages so the next LLM call sees them as fresh user
		// input. Each drained message is persisted via the UserPromptSubmit
		// hook (with steered=true payload) so transcript reload renders them
		// in order with the rest of the conversation.
		l.drainSteer(steerCh, s)

		llmEvents := make(chan llm.StreamEvent, 64)
		var resp llm.Response
		var streamErr error
		streamDone := make(chan struct{})

		// Snapshot the provider once per iteration — a Settings PUT that
		// swaps mid-turn will affect the *next* iteration, not this one,
		// keeping the in-flight stream coherent.
		provider := l.Provider()
		go func() {
			defer close(streamDone)
			resp, streamErr = provider.Stream(ctx, model, systemPrompt, s.Snapshot(), l.tools.Definitions(), llmEvents)
			close(llmEvents)
		}()

		var partialText strings.Builder
		for ev := range llmEvents {
			switch ev.Kind {
			case llm.StreamText:
				partialText.WriteString(ev.TextDelta)
				emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: ev.TextDelta})
			case llm.StreamThinking:
				emit(out, RunEvent{Kind: EventThinking, SessionID: s.ID, ThinkingDelta: ev.ThinkingDelta})
			case llm.StreamError:
				emit(out, RunEvent{Kind: EventError, SessionID: s.ID, Error: ev.Err})
			}
		}

		<-streamDone

		if streamErr != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				partial := strings.TrimSpace(partialText.String())
				if partial != "" {
					s.Append(llm.Message{Role: llm.RoleAssistant, Content: partial})
					l.fireHook("TaskCompleted", s.ID, s.Project, partial, map[string]any{
						"interrupted": true,
					})
				}
				emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, StopReason: "interrupted"})
				return nil
			}
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
			// Auto-name the session after the first complete exchange.
			// MaybeName is cheap when the session is already named (one
			// indexed lookup); it runs Haiku async only when we need a
			// fresh title. Safe to call on every turn.
			if l.namer != nil {
				l.namer.MaybeName(s.ID, userMsg, resp.Text)
			}
			return nil
		}

		s.Append(llm.Message{Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls})

		for _, tc := range resp.ToolCalls {
			startedAt := time.Now().UTC()
			l.fireHook("PreToolUse", s.ID, s.Project, tc.Name, map[string]any{
				"name":         tc.Name,
				"input":        tc.Input,
				"tool_call_id": tc.ID,
			})

			decision := l.gate.Authorize(ctx, s.ID, s.Project, tc.Name, tc.Input)

			// Surface the tool call to Studio. When the gate parked it on
			// a contract, Studio renders inline Approve/Deny buttons so
			// the boss can decide right in the chat. Otherwise the card
			// shows the spinner / "running" state until the result lands.
			emit(out, RunEvent{
				Kind:      EventToolCall,
				SessionID: s.ID,
				ToolCall: &ToolEvent{
					ID:               tc.ID,
					Name:             tc.Name,
					Input:            tc.Input,
					StartedAt:        startedAt,
					AwaitingApproval: !decision.Allow && decision.WaitForApproval && decision.ContractID != "",
					ContractID:       decision.ContractID,
					Preview:          decision.Preview,
				},
			})

			var (
				output  string
				execErr error
				endedAt time.Time
			)

			switch {
			case decision.Allow:
				output, execErr = l.tools.Execute(ctx, tc)
				endedAt = time.Now().UTC()

			case decision.WaitForApproval && decision.ContractID != "":
				// Block on the gate's wait-for-approval channel. The
				// inline buttons in Studio POST to /api/trust-contracts
				// to flip the row's status; WaitForDecision returns when
				// that lands. On approve we run the same tool call we
				// were going to run — output streams into the SAME card.
				timeout := decision.WaitTimeout
				if timeout <= 0 {
					timeout = 15 * time.Minute
				}
				l.fireHook("ToolGated", s.ID, s.Project, tc.Name+": "+decision.Reason, map[string]any{
					"name":        tc.Name,
					"input":       tc.Input,
					"reason":      decision.Reason,
					"contract_id": decision.ContractID,
				})
				approved, reason := l.gate.WaitForDecision(ctx, decision.ContractID, timeout)
				if approved {
					output, execErr = l.tools.Execute(ctx, tc)
				} else {
					if reason == "" {
						reason = "denied or expired"
					}
					output = "BLOCKED: " + tc.Name + " " + reason + "\nThe boss did not approve this call; do not retry without a fresh request."
				}
				endedAt = time.Now().UTC()

			default:
				endedAt = time.Now().UTC()
				output = formatGatedOutput(tc.Name, decision)
				l.fireHook("ToolGated", s.ID, s.Project, tc.Name+": "+decision.Reason, map[string]any{
					"name":        tc.Name,
					"input":       tc.Input,
					"reason":      decision.Reason,
					"contract_id": decision.ContractID,
				})
			}

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
				"name":         tc.Name,
				"input":        tc.Input,
				"output":       output,
				"tool_call_id": tc.ID,
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

// drainSteer pulls every queued steer message off the channel non-blockingly
// and appends each as a User turn on the session, mirroring the same hook
// the WS path fires for a normal message. Called between iterations so the
// next LLM call sees the steer input alongside the original prompt and any
// intermediate tool results. Empty/whitespace strings are dropped.
func (l *Loop) drainSteer(ch <-chan string, s *Session) {
	if ch == nil || s == nil {
		return
	}
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			text := strings.TrimSpace(msg)
			if text == "" {
				continue
			}
			s.Append(llm.Message{Role: llm.RoleUser, Content: text})
			l.fireHook("UserPromptSubmit", s.ID, s.Project, text, map[string]any{
				"steered": true,
			})
		default:
			return
		}
	}
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

// formatGatedOutput is the synthetic tool result shown to the LLM when a
// gate blocks execution. It tells the model what actually happened —
// success-or-failure honesty matters here because the model will paraphrase
// the result to the user, and if we lie ("queued") when the row was never
// persisted the user gets a phantom Trust contract that never appears.
func formatGatedOutput(toolName string, d GateDecision) string {
	var b strings.Builder
	b.WriteString("BLOCKED: tool ")
	b.WriteString(toolName)
	b.WriteString(" requires the boss's approval before running.\n")
	if d.Reason != "" {
		b.WriteString("Reason: ")
		b.WriteString(d.Reason)
		b.WriteString("\n")
	}
	if d.ContractID != "" {
		b.WriteString("Trust contract: ")
		b.WriteString(d.ContractID)
		b.WriteString("\n")
		b.WriteString("This call IS queued in the Trust tab. Tell the boss to approve there. Do NOT retry without approval.")
	} else {
		b.WriteString("WARNING: this call was NOT persisted to the Trust queue (no contract id). ")
		b.WriteString("DO NOT tell the boss it was queued — the gate fired but the row failed to land. ")
		b.WriteString("Tell the boss the Trust store is misconfigured and the action was simply refused.")
	}
	return b.String()
}
