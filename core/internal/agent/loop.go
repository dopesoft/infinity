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
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
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

	// Real API-reported usage from the most recent completed turn — fed
	// straight from the LLM provider's Response.Usage. lastInputTokens
	// represents the current context-window fill (= what the API counted
	// on the last call); the context meter reads this so the meter shows
	// 0 on empty sessions and only grows when a turn has actually fired.
	lastInputTokens   int
	lastOutputTokens  int
	totalInputTokens  int
	totalOutputTokens int

	// Active is the per-session whitelist of tools whose full schemas are
	// shipped to the LLM each turn. Everything else lives in the dormant
	// catalog (one line in the system prompt) and is loadable on demand
	// via the load_tools native tool. See tools/active_set.go for the
	// full semantics including TTL decay and pinning. Initialised in
	// GetOrCreateSession with the curated default loadout.
	Active *tools.ActiveSet

	// SystemPromptOverride replaces the loop's base soul prompt for this
	// session only. The memory prefix + skills prefix + tool catalog still
	// stack above it — only the constant "you are Jarvis" portion is
	// swapped. Used by the delegate tool to apply a persona to a child
	// session without forking the whole agent loop.
	SystemPromptOverride string
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

// ReplaceMessages atomically swaps the session's message history. Used
// by the conversation compactor to drop older turns after they've been
// promoted to mem_observations. The caller is responsible for ensuring
// the new list is coherent (e.g. doesn't strand a tool result without
// its preceding call).
func (s *Session) ReplaceMessages(next []llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = next
}

// RecordUsage updates the session's API-reported token counters after a
// turn completes. Called by the loop with whatever the provider returned in
// Response.Usage. Safe to call with zero values — turns that erred before
// the LLM responded simply don't move the counters.
func (s *Session) RecordUsage(u llm.TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.Input > 0 {
		s.lastInputTokens = u.Input
		s.totalInputTokens += u.Input
	}
	if u.Output > 0 {
		s.lastOutputTokens = u.Output
		s.totalOutputTokens += u.Output
	}
}

// UsageSnapshot returns the API-reported counters for this session. Used by
// the context meter to render "real" fill instead of preview estimates.
type UsageSnapshot struct {
	LastInputTokens   int
	LastOutputTokens  int
	TotalInputTokens  int
	TotalOutputTokens int
}

func (s *Session) UsageSnapshot() UsageSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return UsageSnapshot{
		LastInputTokens:   s.lastInputTokens,
		LastOutputTokens:  s.lastOutputTokens,
		TotalInputTokens:  s.totalInputTokens,
		TotalOutputTokens: s.totalOutputTokens,
	}
}

// SeedUsage installs counters from persistent storage when a session is
// faulted back into the in-memory map after a process restart. Unlike
// RecordUsage, this overwrites unconditionally (including zero values)
// and replaces totals rather than incrementing — the persisted row is
// already the cumulative truth.
func (s *Session) SeedUsage(snap UsageSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInputTokens = snap.LastInputTokens
	s.lastOutputTokens = snap.LastOutputTokens
	s.totalInputTokens = snap.TotalInputTokens
	s.totalOutputTokens = snap.TotalOutputTokens
}

// MemoryProvider lets memory inject relevant retrievals without coupling.
type MemoryProvider interface {
	BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error)
}

// UsageStore persists per-session API-reported token counters across
// process restarts. The agent loop records every successful turn's
// Usage.Input/Output onto Session.{last,total}{Input,Output}Tokens — those
// fields live in process memory, so without persistence Railway's nightly
// container rotation wipes them and Studio's context meter shows 0% on
// sessions that very much aren't empty.
//
// Implementations live in core/internal/sessions (backed by mem_sessions).
// All methods must be safe to call concurrently. Hydrate returning a zero
// snapshot + nil error means "no row yet" — that's the signal that this
// session has never recorded usage, not an error.
type UsageStore interface {
	Hydrate(ctx context.Context, sessionID string) (UsageSnapshot, error)
	Save(ctx context.Context, sessionID string, snap UsageSnapshot) error
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

// AccountResolver injects the "which third-party accounts are connected"
// system-prompt block. Implementation lives in core/internal/connectors;
// declared here to keep the agent package free of pgx/http deps for
// that subsystem. Nil-safe — when unset the loop simply doesn't add
// the block and the model loses awareness of multi-account routing.
type AccountResolver interface {
	SystemPromptBlock() string
}

type Loop struct {
	// providerMu guards llmProvider for hot-swap from Settings PUTs. We
	// take a Read-lock on every Stream call to grab the current provider,
	// then drop the lock before doing I/O — keeps the swap path cheap and
	// concurrent turns safe.
	providerMu  sync.RWMutex
	llmProvider llm.Provider

	tools    *tools.Registry
	memory   MemoryProvider
	hooks    HookEmitter
	skills   SkillMatcher
	gate     ToolGate
	namer    SessionNamer
	accounts AccountResolver

	// usageStore persists session token counters so the context meter
	// survives restarts. Nil-safe: when unset the loop simply doesn't
	// hydrate or persist and the meter falls back to its pre-013
	// behavior (0% after restart).
	usageStoreMu sync.RWMutex
	usageStore   UsageStore

	mu       sync.Mutex
	sessions map[string]*Session

	systemPrompt      string
	maxToolIterations int

	// compactor handles automatic conversation compaction when a turn's
	// reported input_tokens crosses the threshold. Nil-safe: when unset
	// (no provider/pool wired) the loop simply never auto-compacts and
	// the model can still trigger compaction manually via the
	// compact_context tool. Set via SetCompactor after construction.
	compactorMu sync.RWMutex
	compactor   *memory.ConversationCompactor

	// autoCompactThreshold is the input-token count above which a turn's
	// successful completion fires a background compaction pass. Default
	// 120_000 (roughly 60% of a 200K window) so we compact before the
	// next turn starts to bloat further. Tune via INFINITY_AUTO_COMPACT_AT.
	autoCompactThreshold int
}

// SetCompactor installs the conversation compactor used by the auto-
// compact path. Safe to call after agent.New() since the loop doesn't
// touch the compactor until the first turn completes.
func (l *Loop) SetCompactor(c *memory.ConversationCompactor) {
	l.compactorMu.Lock()
	defer l.compactorMu.Unlock()
	l.compactor = c
}

// maybeAutoCompact fires a background compaction pass when the most
// recent turn's input-token count crossed the configured threshold AND
// a compactor is wired AND the session has enough history to bother. Runs
// async (detached context) so the user-visible response isn't delayed.
//
// Concurrency: ReplaceMessages takes the session's mutex, so a turn that
// starts before the goroutine finishes will see either the pre- or
// post-compaction message list — never a torn intermediate state.
func (l *Loop) maybeAutoCompact(s *Session, lastInputTokens int) {
	if l.autoCompactThreshold <= 0 || lastInputTokens < l.autoCompactThreshold {
		return
	}
	l.compactorMu.RLock()
	c := l.compactor
	l.compactorMu.RUnlock()
	if c == nil {
		return
	}
	go func() {
		// Detached context with a generous deadline — compaction is
		// network-bound on the summariser call but should never run
		// longer than a minute or two.
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		newMsgs, res, err := c.Compact(ctx, s.ID, s.Snapshot(), &memory.CompactionConfig{})
		if err != nil {
			log.Printf("auto-compact: session=%s err=%v", s.ID, err)
			return
		}
		if res.CompactedTurns == 0 {
			return
		}
		s.ReplaceMessages(newMsgs)
		log.Printf("auto-compact: session=%s compacted %d turns, kept %d, %d observations promoted",
			s.ID, res.CompactedTurns, res.KeptTurns, len(res.ObservationIDs))
	}()
}

type Config struct {
	LLM               llm.Provider
	Tools             *tools.Registry
	Memory            MemoryProvider
	Hooks             HookEmitter
	Skills            SkillMatcher
	Gate              ToolGate
	Namer             SessionNamer
	Accounts          AccountResolver
	UsageStore        UsageStore
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
	threshold := 120_000
	if v := strings.TrimSpace(os.Getenv("INFINITY_AUTO_COMPACT_AT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
		}
	}
	return &Loop{
		llmProvider:          cfg.LLM,
		tools:                cfg.Tools,
		memory:               cfg.Memory,
		hooks:                cfg.Hooks,
		skills:               cfg.Skills,
		gate:                 cfg.Gate,
		namer:                cfg.Namer,
		accounts:             cfg.Accounts,
		systemPrompt:         cfg.SystemPrompt,
		maxToolIterations:    cfg.MaxToolIterations,
		sessions:             make(map[string]*Session),
		autoCompactThreshold: threshold,
		usageStore:           cfg.UsageStore,
	}
}

// SetUsageStore installs (or replaces) the persistence backing for
// session token counters. Safe to call after agent.New() since the
// loop reads the store under an RWMutex on every hydrate/persist.
func (l *Loop) SetUsageStore(s UsageStore) {
	l.usageStoreMu.Lock()
	defer l.usageStoreMu.Unlock()
	l.usageStore = s
}

func (l *Loop) UsageStore() UsageStore {
	l.usageStoreMu.RLock()
	defer l.usageStoreMu.RUnlock()
	return l.usageStore
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

// SystemPrompt returns the constant system prompt (soul + base). Memory and
// skills prefixes are added per-turn and aren't included here; the context
// meter API uses this for the static portion of its breakdown.
func (l *Loop) SystemPrompt() string { return l.systemPrompt }

// Skills returns the loop's skill matcher so callers can compute the
// skill-prefix contribution to context. Nil-safe.
func (l *Loop) Skills() SkillMatcher { return l.skills }

func (l *Loop) GetOrCreateSession(id string) *Session {
	l.mu.Lock()
	if id == "" {
		id = uuid.NewString()
	}
	s, ok := l.sessions[id]
	created := false
	if !ok {
		s = &Session{
			ID:        id,
			StartedAt: time.Now().UTC(),
			Active:    tools.NewDefaultActiveSet(),
		}
		l.sessions[id] = s
		created = true
	}
	if s.Active == nil {
		// Defensive — older sessions reattached after a process restart
		// might not have an ActiveSet yet. Backfill with the default.
		s.Active = tools.NewDefaultActiveSet()
	}
	l.mu.Unlock()

	if created {
		// Best-effort hydrate of persisted token counters. We deliberately
		// run this outside l.mu so a slow DB doesn't stall every other
		// session lookup. The lookup is keyed by PK — sub-ms on a healthy
		// pool — but the timeout caps the worst case.
		if store := l.UsageStore(); store != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			snap, err := store.Hydrate(ctx, id)
			cancel()
			if err != nil {
				log.Printf("usage hydrate: session=%s err=%v", id, err)
			} else {
				s.SeedUsage(snap)
			}
		}
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
	if override := strings.TrimSpace(s.SystemPromptOverride); override != "" {
		systemPrompt = override
	}
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
	// Prepend the dormant tool catalog so the model knows what exists
	// even when it doesn't have the schema in hand. Cheap (~30 tokens
	// per entry) and unlocks the tool_search → load_tools loop.
	if catalog := buildToolCatalogBlock(l.tools, s.Active); catalog != "" {
		systemPrompt = catalog + "\n\n" + systemPrompt
	}
	// Prepend the connected-accounts overlay so the model can route to
	// the right OAuth account when a tool has multi-account support
	// (e.g. four Gmail mailboxes). The block lists per-toolkit
	// alias → account_id mappings; the model picks based on the user's
	// stated intent.
	if l.accounts != nil {
		if accountsBlock := l.accounts.SystemPromptBlock(); accountsBlock != "" {
			systemPrompt = accountsBlock + "\n\n" + systemPrompt
		}
	}

	for iter := 0; iter < l.maxToolIterations; iter++ {
		// Age out TTL'd entries before the next LLM call — keeps an
		// exploratory `load_tools` from squatting once the relevant work
		// is done.
		s.Active.DecayTTL()
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
		// Only ship schemas for tools currently in the session's active
		// set — the dormant long tail lives in the system-prompt catalog
		// block and surfaces via tool_search. This is the core Phase-1
		// context-budget win.
		toolDefs := l.tools.DefinitionsFor(s.Active.Names())
		go func() {
			defer close(streamDone)
			resp, streamErr = provider.Stream(ctx, model, systemPrompt, s.Snapshot(), toolDefs, llmEvents)
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

		// Record real API-reported usage on every successful stream. The
		// context meter reads s.lastInputTokens to show current window
		// fill — 0 on empty sessions, accurate after each turn.
		s.RecordUsage(resp.Usage)
		// Persist counters so a process restart doesn't reset the meter
		// to 0% on a session with real history. Best-effort + detached
		// context so the user-visible turn isn't gated on the DB write.
		if store := l.UsageStore(); store != nil && (resp.Usage.Input > 0 || resp.Usage.Output > 0) {
			snap := s.UsageSnapshot()
			sessionID := s.ID
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := store.Save(ctx, sessionID, snap); err != nil {
					log.Printf("usage persist: session=%s err=%v", sessionID, err)
				}
			}()
		}

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
			// Auto-compaction: if this turn's reported input crossed the
			// threshold, run compaction in the background so the *next*
			// turn lands on a tighter buffer. We don't block the return
			// because the user's response has already streamed.
			l.maybeAutoCompact(s, resp.Usage.Input)
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
				// Inject the session's ActiveSet so session-aware tools
				// (load_tools / unload_tools / compact_context) can
				// mutate the right session's loaded list.
				toolCtx := tools.WithActiveSet(ctx, s.Active)
				output, execErr = l.tools.Execute(toolCtx, tc)
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
					toolCtx := tools.WithActiveSet(ctx, s.Active)
					output, execErr = l.tools.Execute(toolCtx, tc)
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
