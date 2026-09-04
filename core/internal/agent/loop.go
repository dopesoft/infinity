// Package agent implements Infinity's intentionally-small agent loop.
// Inspired by nanobot's design: receive prompt → build context → call LLM →
// dispatch tools → repeat until the model returns text.
//
// The memory subsystem attaches via MemoryProvider (search) and the hooks
// pipeline via HookEmitter (capture).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/turnctx"
	"github.com/google/uuid"
)

// infoLog writes to stdout so Railway tags these lines severity=info
// instead of the severity=error it stamps on stderr (stdlib log's default).
// Reserve the default log.Printf for genuine failures.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// SkillMatcher is implemented by skills.Registry. Decoupled to keep the agent
// package free of skill-package dependencies.
type SkillMatcher interface {
	MatchAndPrefix(message string, limit int) string
}

// maxDelegateSpawnsPerTurn hard-caps how many sub-agents a single turn can
// spawn. Defends against a runaway model firing dozens of `delegate` calls at
// once (the storm that left 100 spinning delegates). Legit parallel work rarely
// needs more than a handful.
const maxDelegateSpawnsPerTurn = 8

// defaultMaxTurnSegments bounds how many times a single turn may exhaust its
// per-segment tool-iteration budget, checkpoint (compact + persist), and run
// on. A genuine "diagnose → implement → test → commit → push" job legitimately
// needs more than one cap's worth of tool calls; rather than hard-erroring at
// the cap and abandoning a half-done plan, the loop compacts its context and
// runs another segment — but only while it's still making progress and only up
// to this many segments, so a stuck model can never spin forever. The durable
// plan/todo substrate is what makes this safe: progress is checkpointed, so a
// continuation resumes exactly where the previous segment left off. Override
// with INFINITY_MAX_TURN_SEGMENTS.
const defaultMaxTurnSegments = 3

// turnWindDownBlock is appended to the system prompt once a turn crosses ~80%
// of its per-segment tool-iteration budget. It nudges the model to land the
// step it's on and stop at a clean boundary — checkpointing to the durable
// plan/todo so a continuation (this turn's next segment, or a later turn)
// resumes exactly there — instead of getting cut off mid-action when the
// budget runs out.
const turnWindDownBlock = `<budget_notice>
You are close to this turn's tool-call budget. Do NOT start a new multi-step
sub-task right now. Finish the action you're on, record where you are with
plan_update / todo_write so nothing is lost, then write one short line on
what's done and what's left and stop. Work resumes automatically from that
checkpoint — a clean handoff at a step boundary beats being cut off mid-action.
</budget_notice>`

// defaultSystemPrompt is the fallback when no soul has been loaded.
// In practice the soul package always supplies one (embedded soul.md);
// this exists only so a misconfigured Loop still has a sane persona.
const defaultSystemPrompt = `You are Jarvis, the boss's personal AI agent running inside Infinity.

You have access to tools. When a tool call moves the work forward, make it. Don't ask permission for routine work and don't narrate the call afterwards, integrate the result into your reply.

Do not use em dash or en dash characters. Use a comma, period, parentheses, colon, or plain hyphen instead.

Be concise. Address the user as "boss". Cite memory sources when you rely on them.`

type Session struct {
	ID        string
	Project   string
	StartedAt time.Time
	Messages  []llm.Message
	mu        sync.Mutex

	// Real API-reported usage from the most recent completed turn - fed
	// straight from the LLM provider's Response.Usage. lastInputTokens
	// represents the current context-window fill (= what the API counted
	// on the last call); the context meter reads this so the meter shows
	// 0 on empty sessions and only grows when a turn has actually fired.
	lastInputTokens   int
	lastOutputTokens  int
	totalInputTokens  int
	totalOutputTokens int
	// Prompt-cache breakdown of the last turn's prompt (already inside
	// lastInputTokens). Lets the context modal show how much of the window
	// was served from cache. Not persisted across restart - repopulates on
	// the next turn, same as the rest of the live usage.
	lastCacheReadTokens  int
	lastCacheWriteTokens int
	// lastMeasuredModel is the model that produced lastInputTokens.
	lastMeasuredModel string

	// replyIndex numbers the assistant messages of the CURRENT turn, from 0.
	// Every delta carries the index of the message it belongs to and every
	// persisted assistant row (AssistantMessage, TaskCompleted) carries the
	// same number, so the browser pairs a live bubble with its row by
	// (turn_id, message_index) instead of by comparing text. Reset per turn;
	// only the turn's own goroutine touches it.
	replyIndex int

	// Active is the per-session whitelist of tools whose full schemas are
	// shipped to the LLM each turn. Everything else lives in the dormant
	// catalog (one line in the system prompt) and is loadable on demand
	// via the load_tools native tool. See tools/active_set.go for the
	// full semantics including TTL decay and pinning. Initialised in
	// GetOrCreateSession with the curated default loadout.
	Active *tools.ActiveSet

	// SystemPromptOverride replaces the loop's base soul prompt for this
	// session only. The memory prefix + skills prefix + tool catalog still
	// stack above it - only the constant "you are Jarvis" portion is
	// swapped. Used by the delegate tool to apply a persona to a child
	// session without forking the whole agent loop.
	SystemPromptOverride string

	// world remembers which world-state sections this session has already
	// been told, so a turn sends the full block once and diffs after. See
	// worldstate.go.
	world worldSnapshot
	// volatileAt is the index of the message carrying this turn's pinned
	// context (Message.Volatile), -1 when none. Cleared when the turn ends.
	volatileAt int
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
// Response.Usage. Safe to call with zero values - turns that erred before
// the LLM responded simply don't move the counters.
func (s *Session) RecordUsage(u llm.TokenUsage, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The context meter must reflect the FULL prompt size (uncached input plus
	// cache reads/writes), so a cache HIT - which reports most of the prompt
	// under CacheRead with a tiny Input - doesn't make the meter read empty on
	// a session that's actually full. Cached tokens still occupy the window and
	// still count to rate limits; only their dollar cost is discounted.
	// WindowTokens, not PromptTokens: for a brain that runs its own tool loop
	// those differ by an order of magnitude, and this counter is what the
	// meter and auto-compaction both read. See TokenUsage.ContextTokens.
	inputTotal := u.WindowTokens()
	if inputTotal > 0 {
		s.lastInputTokens = inputTotal
		s.totalInputTokens += inputTotal
		// Record the cache split of this turn alongside it (only when the
		// turn actually reported usage, so an erred pre-LLM turn doesn't
		// zero out the last good reading).
		s.lastCacheReadTokens = u.CacheRead
		s.lastCacheWriteTokens = u.CacheWrite
		// WHICH BRAIN this measurement came from. A fill is a measurement of
		// one prompt sent to one model, and the boss switches models inside a
		// live conversation: a 900K reading taken on one brain says nothing
		// about how full the next brain's window is, and rendering it against
		// that brain's number is how the meter showed him a full red bar on a
		// window he had barely touched.
		s.lastMeasuredModel = strings.TrimSpace(model)
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
	// Cache split of the last turn (subset of LastInputTokens). 0 when the
	// model/turn didn't cache, so the modal reads accurately on every model.
	LastCacheReadTokens  int
	LastCacheWriteTokens int
	// LastMeasuredModel is the model that produced LastInputTokens. Empty
	// when unknown. A reading taken on a different brain than the one now
	// answering is not this brain's fill and must not be shown as it.
	LastMeasuredModel string
}

func (s *Session) UsageSnapshot() UsageSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return UsageSnapshot{
		LastInputTokens:      s.lastInputTokens,
		LastOutputTokens:     s.lastOutputTokens,
		TotalInputTokens:     s.totalInputTokens,
		TotalOutputTokens:    s.totalOutputTokens,
		LastCacheReadTokens:  s.lastCacheReadTokens,
		LastCacheWriteTokens: s.lastCacheWriteTokens,
		LastMeasuredModel:    s.lastMeasuredModel,
	}
}

// InvalidateUsage voids the last fill measurement.
//
// Called when the thing that was measured stops existing: the thread has just
// been compacted, so the number describing how full the window was is about a
// conversation that is no longer there. Leaving it up is what made the meter
// sit red straight after a compaction that had just freed most of the window.
// The next turn measures the real thing.
func (s *Session) InvalidateUsage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInputTokens = 0
	s.lastCacheReadTokens = 0
	s.lastCacheWriteTokens = 0
	s.lastMeasuredModel = ""
}

// SeedUsage installs counters from persistent storage when a session is
// faulted back into the in-memory map after a process restart. Unlike
// RecordUsage, this overwrites unconditionally (including zero values)
// and replaces totals rather than incrementing - the persisted row is
// already the cumulative truth.
func (s *Session) SeedUsage(snap UsageSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInputTokens = snap.LastInputTokens
	s.lastOutputTokens = snap.LastOutputTokens
	s.totalInputTokens = snap.TotalInputTokens
	s.totalOutputTokens = snap.TotalOutputTokens
	s.lastCacheReadTokens = snap.LastCacheReadTokens
	s.lastMeasuredModel = snap.LastMeasuredModel
	s.lastCacheWriteTokens = snap.LastCacheWriteTokens
}

// MemoryProvider lets memory inject relevant retrievals without coupling.
type MemoryProvider interface {
	BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error)
}

// UsageStore persists per-session API-reported token counters across
// process restarts. The agent loop records every successful turn's
// Usage.Input/Output onto Session.{last,total}{Input,Output}Tokens - those
// fields live in process memory, so without persistence Railway's nightly
// container rotation wipes them and Studio's context meter shows 0% on
// sessions that very much aren't empty.
//
// Implementations live in core/internal/sessions (backed by mem_sessions).
// All methods must be safe to call concurrently. Hydrate returning a zero
// snapshot + nil error means "no row yet" - that's the signal that this
// session has never recorded usage, not an error.
type UsageStore interface {
	Hydrate(ctx context.Context, sessionID string) (UsageSnapshot, error)
	Save(ctx context.Context, sessionID string, snap UsageSnapshot) error
}

// HookEmitter is implemented by hooks.Pipeline. Decoupled here.
type HookEmitter interface {
	Emit(name string, sessionID, project, text string, payload map[string]any)
}

// TurnRecorder is the LangSmith-style trace persistence boundary. The loop
// opens a row at turn entry, threads the returned turn_id through every
// `fireHook` payload, increments the tool-call counter on each PreToolUse,
// and closes the row with the final outcome on TaskCompleted. Implemented
// by *memory.TurnStore - decoupled so the agent package doesn't pull pgx.
// Nil-safe: when unset, the loop simply never writes to mem_turns and the
// /logs UI shows nothing for those sessions.
type TurnRecorder interface {
	Open(ctx context.Context, sessionID, userText, model string) (string, error)
	Close(ctx context.Context, turnID string, fields TurnCloseFields) error
	IncrementToolCalls(ctx context.Context, turnID string) error
}

// TurnCloseFields mirrors memory.CloseFields but lives in the agent package
// so the recorder interface doesn't drag memory imports here.
type TurnCloseFields struct {
	AssistantText string
	StopReason    string
	InputTokens   int
	OutputTokens  int
	// CacheReadTokens / CacheWriteTokens carry the prompt-cache breakdown of
	// this turn's prompt (already counted inside InputTokens). 0 = no cache.
	CacheReadTokens  int
	CacheWriteTokens int
	ToolCallCount    int
	Status           string
	Error            string
	Summary          string
}

type CostRecorder interface {
	RecordCost(ctx context.Context, category, subject string, costUSD float64, units string, quantity float64, note string) error
}

// ReauthParker parks a turn that failed because the active model's credential
// needs re-authentication (revoked OAuth token, invalid API key), so a
// background poller can replay it once the brain is healthy again — and
// surfaces a reconnect message into the chat. Implemented in the serve command
// (persist to mem_reauth_waits + notify the session). Nil-safe: when unset the
// loop falls back to the normal error path.
type ReauthParker interface {
	Park(ctx context.Context, sessionID, provider, model, userText, reason string) error
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
// that subsystem. Nil-safe - when unset the loop simply doesn't add
// the block and the model loses awareness of multi-account routing.
type AccountResolver interface {
	SystemPromptBlock() string
}

type Loop struct {
	// providerMu guards llmProvider for hot-swap from Settings PUTs. We
	// take a Read-lock on every Stream call to grab the current provider,
	// then drop the lock before doing I/O - keeps the swap path cheap and
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

	// turnsMu guards turns. Hot-swappable via SetTurnRecorder so wiring
	// order at boot doesn't matter; the loop just no-ops on nil.
	turnsMu sync.RWMutex
	turns   TurnRecorder

	// planMu guards planChecker + planSettler — the plan-continuation backstop
	// (keep executing a drafted plan instead of stopping cold) and the
	// settle-on-turn-end mechanic (close the foreground plan the instant the turn
	// ends so the reaper never false-fails a step). Hot-swappable; nil-safe.
	planMu      sync.RWMutex
	planChecker PlanContinuationChecker
	planSettler PlanSettler
	// planStarter marks the session's current plan step in flight just before a
	// consequential tool runs, so the board can never show "nothing started"
	// while real work is happening (see plan_start.go). Shares planMu.
	planStarter PlanStepStarter
	// proposalFiler records source changes the self-heal guard refused to
	// make live (see selfheal.go); shares planMu.
	proposalFiler CodeProposalFiler

	// reauthMu guards reauth. When the active brain hits a credential failure
	// the loop parks the turn via this hook (instead of dumping a raw 401) and
	// the reauth poller replays it once the credential is healthy. Nil-safe +
	// hot-swap via SetReauthParker.
	reauthMu sync.RWMutex
	reauth   ReauthParker

	// usageStore persists session token counters so the context meter
	// survives restarts. Nil-safe: when unset the loop simply doesn't
	// hydrate or persist and the meter falls back to its pre-013
	// behavior (0% after restart).
	usageStoreMu sync.RWMutex
	usageStore   UsageStore

	costMu sync.RWMutex
	costs  CostRecorder

	// projectFetcher resolves a session's ACTIVE project (a short label derived
	// from mem_sessions.project_path) at the start of each turn, so observations
	// captured during the turn are tagged with the project being worked on. Was
	// always empty before — memory wasn't project-aware. Nil-safe + hot-swap.
	projectMu      sync.RWMutex
	projectFetcher func(ctx context.Context, sessionID string) string

	mu       sync.Mutex
	sessions map[string]*Session

	// runCancels lets an out-of-band caller (the Stop button on the Agent
	// Work board) abort an in-flight turn by session id. Every Run registers
	// its cancel func here for its duration; CancelSession looks it up and
	// fires it, tearing the turn down at the next ctx check. The generation
	// token guards against a finished turn's deferred clear deleting a newer
	// turn's entry on the same session. Guarded by cancelMu.
	cancelMu   sync.Mutex
	cancelGen  uint64
	runCancels map[string]*runCancelEntry

	systemPrompt      string
	maxToolIterations int
	maxTurnSegments   int

	// compactor handles automatic conversation compaction when a turn's
	// reported input_tokens crosses the threshold. Nil-safe: when unset
	// (no provider/pool wired) the loop simply never auto-compacts and
	// the model can still trigger compaction manually via the
	// compact_context tool. Set via SetCompactor after construction.
	compactorMu sync.RWMutex
	compactor   *memory.ConversationCompactor

	// autoCompactThreshold is the fallback input-token count above which a
	// turn's successful completion fires a background compaction pass. The
	// live number comes from compactAt(), which sizes it against the ACTIVE
	// brain's window; this is what it falls back to when there is no brain to
	// ask. Pinned means the boss set INFINITY_AUTO_COMPACT_AT himself, and
	// then his number is the one that is used.
	autoCompactThreshold       int
	autoCompactThresholdPinned bool

	// toolVisibility is the per-turn hook that decides which tool names
	// to hide from the model for a given session. Guarded by providerMu
	// (shares the same hot-swap lock as the LLM provider - both are
	// per-turn snapshots read once per iteration). Nil-safe.
	toolVisibility ToolVisibilityFunc

	// activeModelFn returns the boss's currently-selected model id from
	// Studio's Settings store. Wired once at boot via SetActiveModelFn.
	// CENTRAL resolver: every Run() call falls back to this when the
	// caller passes an empty model string, so cron, workflow executor,
	// delegate, heartbeat, voice tool turns - every code path that runs
	// the agent - automatically honors the boss's selection without
	// having to plumb settings through call by call. The provider boot
	// default (e.g. gpt-5-codex on openai_oauth) is the LAST resort,
	// not the silent default. Guarded by providerMu since it's read on
	// every turn alongside the live provider.
	activeModelFn func(ctx context.Context) string

	// effortFn resolves the per-turn reasoning-effort level (steal C) from the
	// session's live signals. Guarded by providerMu like activeModelFn. Nil ->
	// no per-turn effort (every turn keeps the model default, i.e. today's
	// behavior). Returns the level to apply ("" = omit) and a short source
	// string for the audit trail / Composer display. NEVER returns or changes a
	// model id - only the compute the same model spends.
	effortFn func(ctx context.Context, req EffortRequest) (llm.Effort, string)

	// verifyDirective is the Lever-3 adversarial-verify recipe text (steal C).
	// DATA, not a Go const (Rule #1b): seeded in infinity_meta, set via
	// SetVerifyDirective, hot-swappable under providerMu. Empty -> the verify
	// pass never fires (fail-safe when the seed migration hasn't run).
	verifyDirective string

	// bridgeRouter lets a failed claude_code__* (Mac-only) tool call fail OVER
	// to the cloud bridge instead of spinning retries to the iteration cap. When
	// a claude_code call errors and the Mac bridge is unhealthy, the loop
	// invalidates the router's health cache (so the next turn routes to Cloud and
	// hides the claude_code__* schemas) and hands the model a structured fallback
	// directive pointing at the cloud primitives. Nil-safe + hot-swap via
	// SetBridgeRouter so boot wiring order doesn't matter. Guarded by providerMu
	// (read once per iteration like the other per-turn snapshots).
	bridgeRouter *bridge.Router
}

// runCancelEntry pairs a turn's cancel func with a generation token so a
// stale deferred clear can't evict a newer turn's entry on the same session.
type runCancelEntry struct {
	gen    uint64
	cancel context.CancelFunc
}

// registerRunCancel records the cancel func for an in-flight turn and returns
// its generation token (passed back to clearRunCancel). Last writer wins for a
// given session — fine because a session runs at most one turn at a time.
func (l *Loop) registerRunCancel(sessionID string, cancel context.CancelFunc) uint64 {
	if l == nil || sessionID == "" {
		return 0
	}
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	if l.runCancels == nil {
		l.runCancels = map[string]*runCancelEntry{}
	}
	l.cancelGen++
	l.runCancels[sessionID] = &runCancelEntry{gen: l.cancelGen, cancel: cancel}
	return l.cancelGen
}

// clearRunCancel removes a turn's entry, but only if it's still the same
// generation (a newer turn on the same session must not be evicted).
func (l *Loop) clearRunCancel(sessionID string, gen uint64) {
	if l == nil || sessionID == "" {
		return
	}
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	if e, ok := l.runCancels[sessionID]; ok && e.gen == gen {
		delete(l.runCancels, sessionID)
	}
}

// CancelSession aborts the in-flight turn for a session (the Stop button). It
// cancels the turn's context, so the loop exits at its next ctx check and the
// run/plan bookkeeping closes through the normal finish path. Returns true when
// a live turn was found and signalled. nil-safe.
func (l *Loop) CancelSession(sessionID string) bool {
	if l == nil || sessionID == "" {
		return false
	}
	l.cancelMu.Lock()
	e, ok := l.runCancels[sessionID]
	l.cancelMu.Unlock()
	if ok && e != nil && e.cancel != nil {
		e.cancel()
		return true
	}
	return false
}

// hiddenForSession reads the visibility hook under providerMu and
// returns the set of tool names to drop from this turn. Returns an
// empty map (not nil) when no hook is wired so call sites can iterate
// without a nil check.
func (l *Loop) hiddenForSession(ctx context.Context, sessionID string) map[string]struct{} {
	if l == nil {
		return map[string]struct{}{}
	}
	l.providerMu.RLock()
	fn := l.toolVisibility
	l.providerMu.RUnlock()
	if fn == nil {
		return map[string]struct{}{}
	}
	out := fn(ctx, sessionID)
	if out == nil {
		return map[string]struct{}{}
	}
	return out
}

// filterToolNames drops any name present in hidden. Order-preserving.
func filterToolNames(names []string, hidden map[string]struct{}) []string {
	if len(hidden) == 0 {
		return names
	}
	out := names[:0:0]
	for _, n := range names {
		if _, drop := hidden[n]; drop {
			continue
		}
		out = append(out, n)
	}
	return out
}

// SetCompactor installs the conversation compactor used by the auto-
// compact path. Safe to call after agent.New() since the loop doesn't
// touch the compactor until the first turn completes.
func (l *Loop) SetCompactor(c *memory.ConversationCompactor) {
	l.compactorMu.Lock()
	defer l.compactorMu.Unlock()
	l.compactor = c
}

// SetActiveModelFn installs the resolver that returns the boss's
// currently-selected model id (Studio Settings store). The loop calls
// it once at the top of every Run when the caller passed an empty
// model string. Nil resolver (or empty return) means "no override" -
// the provider's boot default applies. Safe to call after construction.
func (l *Loop) SetActiveModelFn(fn func(ctx context.Context) string) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.activeModelFn = fn
}

// resolveActiveModel reads the active-model setting under providerMu and
// returns the trimmed id (or empty string if unset / no resolver wired).
func (l *Loop) resolveActiveModel(ctx context.Context) string {
	if l == nil {
		return ""
	}
	l.providerMu.RLock()
	fn := l.activeModelFn
	l.providerMu.RUnlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(ctx))
}

// maybeAutoCompact fires a background compaction pass when the most
// recent turn's input-token count crossed the configured threshold AND
// compactAt is the input-token count above which the thread starts getting
// summarised away.
//
// It follows the brain that is ANSWERING, because the boss switches brains
// mid-conversation and the fixed number did not move with him: 120K is a
// sensible 60% of a 200K window and an absurd 12% of the 1M one his plan
// actually runs, so a switch to the big brain meant his thread was being
// compacted away with 88% of the room still free. The other direction is
// worse - a switch DOWN to a small window with a large thread has to compact
// sooner, not at the same place. INFINITY_AUTO_COMPACT_AT still wins when he
// has set it, because a number he chose outranks one we derived.
func (l *Loop) compactAt() int {
	if l.autoCompactThresholdPinned {
		return l.autoCompactThreshold
	}
	p := l.Provider()
	if p == nil {
		return l.autoCompactThreshold
	}
	window := llm.ContextWindow(p.Model())
	if window <= 0 {
		return l.autoCompactThreshold
	}
	return window * 60 / 100
}

// a compactor is wired AND the session has enough history to bother. Runs
// async (detached context) so the user-visible response isn't delayed.
//
// Concurrency: ReplaceMessages takes the session's mutex, so a turn that
// starts before the goroutine finishes will see either the pre- or
// post-compaction message list - never a torn intermediate state.
func (l *Loop) maybeAutoCompact(s *Session, lastInputTokens int) {
	if threshold := l.compactAt(); threshold <= 0 || lastInputTokens < threshold {
		return
	}
	l.compactorMu.RLock()
	c := l.compactor
	l.compactorMu.RUnlock()
	if c == nil {
		return
	}
	provider := l.Provider()
	stable := l.systemPrompt
	if o := strings.TrimSpace(s.SystemPromptOverride); o != "" {
		stable = o
	}
	go func() {
		// Detached context with a generous deadline - compaction is
		// network-bound on the summariser call but should never run
		// longer than a minute or two.
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cfg := &memory.CompactionConfig{Force: true, StableSystem: stable, Tools: l.tools.DefinitionsFor(s.Active.Names())}
		newMsgs, res, err := c.Compact(ctx, s.ID, s.Snapshot(), cfg)
		if err != nil {
			log.Printf("auto-compact: session=%s err=%v", s.ID, err)
			return
		}
		if res.CompactedTurns == 0 {
			return
		}
		s.ReplaceMessages(newMsgs)
		// A brain that holds the conversation itself must drop its session
		// too, or the next resumed turn continues on the uncompacted one.
		if llm.ForgetSessionIfSupported(ctx, provider, s.ID) {
			infoLog.Printf("auto-compact: session=%s brain session dropped; next turn starts from the compacted transcript", s.ID)
		}
		// The fill reading described the thread that was just summarised
		// away, so it is no longer about anything. Void it: the meter reads
		// "measured after your next message" instead of sitting red on a
		// window that was just emptied.
		s.InvalidateUsage()
		infoLog.Printf("auto-compact: session=%s compacted %d turns, kept %d, %d observations promoted",
			s.ID, res.CompactedTurns, res.KeptTurns, len(res.ObservationIDs))
	}()
}

// compactTurnNow runs one SYNCHRONOUS compaction pass and swaps in the tighter
// message list. Returns true when it actually compacted. The per-turn
// continuation path uses this between segments: before running another
// segment we shrink the context so the new segment starts on a tight buffer
// instead of inheriting the bloated history that filled the previous one.
// Synchronous (unlike maybeAutoCompact) because the next segment must see the
// compacted buffer, not race it. No-op + false when no compactor is wired.
func (l *Loop) compactTurnNow(parent context.Context, s *Session, provider llm.Provider, stableSystem string, toolDefs []llm.ToolDef) bool {
	l.compactorMu.RLock()
	c := l.compactor
	l.compactorMu.RUnlock()
	if c == nil {
		return false
	}
	// Detached from the turn's deadline (a compaction that starts near the
	// end of a long turn must still finish) but carrying its values.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 90*time.Second)
	defer cancel()
	cfg := &memory.CompactionConfig{Force: true, StableSystem: stableSystem, Tools: toolDefs}
	newMsgs, res, err := c.Compact(ctx, s.ID, s.Snapshot(), cfg)
	if err != nil {
		log.Printf("turn-continuation compact: session=%s err=%v", s.ID, err)
		return false
	}
	if res.CompactedTurns == 0 {
		return false
	}
	s.ReplaceMessages(newMsgs)
	s.InvalidateUsage()
	if llm.ForgetSessionIfSupported(ctx, provider, s.ID) {
		infoLog.Printf("compact: session=%s brain session dropped; next turn starts from the compacted transcript", s.ID)
	}
	infoLog.Printf("turn-continuation compact: session=%s compacted %d turns, kept %d, %d observations promoted",
		s.ID, res.CompactedTurns, res.KeptTurns, len(res.ObservationIDs))
	return true
}

func (l *Loop) recordLLMCost(provider llm.Provider, model string, usage llm.TokenUsage) {
	rec := l.costRecorder()
	if rec == nil || usage.ThroughputTokens() == 0 {
		return
	}
	subject := strings.TrimSpace(model)
	if subject == "" && provider != nil {
		subject = provider.Model()
	}
	if provider != nil {
		subject = provider.Name() + ":" + subject
	}
	// Ledger semantics (kept honest and distinct):
	//   quantity = ThroughputTokens - the RAW tokens that moved through the
	//              model (full prompt + output, face value). This is what
	//              "how many tokens did I use" sums, and it stays consistent
	//              with pre-caching history.
	//   cost     = estimate over BilledTokens - the cache-DISCOUNTED figure
	//              (reads 0.1x, writes 1.25x). Cheaper than throughput on a
	//              cache hit; that gap is the savings.
	throughput := usage.ThroughputTokens()
	cost := estimateLLMCostUSD(usage.BilledTokens())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.RecordCost(ctx, "llm", subject, cost, "tokens", float64(throughput), "automatic token usage capture")
	}()
}

func (l *Loop) recordToolCost(name string, d time.Duration, execErr error) {
	rec := l.costRecorder()
	if rec == nil {
		return
	}
	note := "automatic tool call capture"
	if execErr != nil {
		note = "automatic failed tool call capture"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.RecordCost(ctx, "tool", name, 0, "milliseconds", float64(d.Milliseconds()), note)
	}()
}

func estimateLLMCostUSD(tokens int) float64 {
	if tokens <= 0 {
		return 0
	}
	rate := 0.0
	if v := strings.TrimSpace(os.Getenv("INFINITY_LLM_COST_PER_1K")); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			rate = parsed
		}
	}
	return float64(tokens) / 1000 * rate
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
	Turns             TurnRecorder
	Costs             CostRecorder
	SystemPrompt      string
	MaxToolIterations int
	// ToolVisibility, when set, decides per-turn which tool names the
	// model is allowed to *see*. The hook returns a set of tool names to
	// hide for the given session - those tools are dropped from both the
	// schema bundle sent to the LLM and the dormant catalog block in the
	// system prompt. Used today by the bridge layer to hide
	// `claude_code__*` (Mac-only) tools when the session is routed to the
	// Cloud workspace, so the model can't accidentally edit the wrong
	// filesystem. Nil-safe: when unset every tool stays visible.
	ToolVisibility ToolVisibilityFunc
}

// ToolVisibilityFunc returns the names that should be hidden from the
// model for this session+turn. Run each iteration; cheap because the
// underlying bridge state is already cached.
type ToolVisibilityFunc func(ctx context.Context, sessionID string) map[string]struct{}

func New(cfg Config) *Loop {
	if cfg.MaxToolIterations <= 0 {
		// Default headroom. 8 was the old value and it was too tight -
		// the agent routinely burns 3–5 iterations on tool discovery
		// (skills_discover, tool_search, load_tools) before any real
		// work happens, and a single multi-step task (delete, verify,
		// confirm) eats 4–6 more. 50 lets the loop actually finish the
		// kind of "do this until it's done" task the boss expects to
		// just work, without becoming so loose it spins forever - token
		// budget + auto-compact still bound the worst case. Override
		// with INFINITY_MAX_TOOL_ITERATIONS for tighter or looser caps.
		cfg.MaxToolIterations = 50
	}
	if v := strings.TrimSpace(os.Getenv("INFINITY_MAX_TOOL_ITERATIONS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxToolIterations = n
		}
	}
	maxSegments := defaultMaxTurnSegments
	if v := strings.TrimSpace(os.Getenv("INFINITY_MAX_TURN_SEGMENTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSegments = n
		}
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
	pinned := false
	if v := strings.TrimSpace(os.Getenv("INFINITY_AUTO_COMPACT_AT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
			pinned = true
		}
	}
	return &Loop{
		llmProvider:                cfg.LLM,
		tools:                      cfg.Tools,
		memory:                     cfg.Memory,
		hooks:                      cfg.Hooks,
		skills:                     cfg.Skills,
		gate:                       cfg.Gate,
		namer:                      cfg.Namer,
		accounts:                   cfg.Accounts,
		systemPrompt:               cfg.SystemPrompt,
		maxToolIterations:          cfg.MaxToolIterations,
		maxTurnSegments:            maxSegments,
		sessions:                   make(map[string]*Session),
		autoCompactThreshold:       threshold,
		autoCompactThresholdPinned: pinned,
		usageStore:                 cfg.UsageStore,
		turns:                      cfg.Turns,
		costs:                      cfg.Costs,
		toolVisibility:             cfg.ToolVisibility,
	}
}

// SetToolVisibility installs (or replaces) the per-turn tool visibility
// hook. Safe to call after agent.New() since the loop snapshots the
// callback under the providerMu lock on every iteration.
func (l *Loop) SetToolVisibility(fn ToolVisibilityFunc) {
	if l == nil {
		return
	}
	l.providerMu.Lock()
	l.toolVisibility = fn
	l.providerMu.Unlock()
}

// SetBridgeRouter installs (or replaces) the bridge router used for Mac->Cloud
// failover on claude_code__* tool errors. Safe to call after agent.New(); the
// loop reads it under providerMu per iteration. Nil is fine (no failover).
func (l *Loop) SetBridgeRouter(r *bridge.Router) {
	if l == nil {
		return
	}
	l.providerMu.Lock()
	l.bridgeRouter = r
	l.providerMu.Unlock()
}

// SetTurnRecorder installs (or replaces) the LangSmith-style trace recorder
// used by the agent loop. Safe to call after agent.New() since the loop
// snapshots the recorder under an RWMutex on every Run.
func (l *Loop) SetTurnRecorder(r TurnRecorder) {
	l.turnsMu.Lock()
	defer l.turnsMu.Unlock()
	l.turns = r
}

func (l *Loop) SetCostRecorder(r CostRecorder) {
	if l == nil {
		return
	}
	l.costMu.Lock()
	l.costs = r
	l.costMu.Unlock()
}

// SetReauthParker installs (or replaces) the model-credential park-and-resume
// hook. Safe to call after agent.New(); the loop snapshots it under an RWMutex.
func (l *Loop) SetReauthParker(p ReauthParker) {
	if l == nil {
		return
	}
	l.reauthMu.Lock()
	l.reauth = p
	l.reauthMu.Unlock()
}

func (l *Loop) reauthParker() ReauthParker {
	if l == nil {
		return nil
	}
	l.reauthMu.RLock()
	defer l.reauthMu.RUnlock()
	return l.reauth
}

func (l *Loop) costRecorder() CostRecorder {
	if l == nil {
		return nil
	}
	l.costMu.RLock()
	defer l.costMu.RUnlock()
	return l.costs
}

func (l *Loop) turnRecorder() TurnRecorder {
	l.turnsMu.RLock()
	defer l.turnsMu.RUnlock()
	return l.turns
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

// MemoryPrefix is the query-conditioned memory prefix the loop would
// otherwise prepend to system prompt for the next turn. Exposed so the
// voice session minter can stamp the same context into the realtime
// ephemeral key. Empty string + nil error when no memory provider is
// wired or when the provider returned empty (cold start, no relevant
// retrievals).
func (l *Loop) MemoryPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if l == nil || l.memory == nil {
		return "", nil
	}
	return l.memory.BuildSystemPrefix(ctx, sessionID, query)
}

// GateForVoice returns the tool gate used by the agent loop. Voice tool
// calls go through the same gate chain as text so high-risk calls land
// in the Trust queue exactly like normal. Falls back to AllowAll when
// no gate was configured at construction.
func (l *Loop) GateForVoice() ToolGate {
	if l == nil || l.gate == nil {
		return AllowAll{}
	}
	return l.gate
}

// Hooks exposes the pipeline emitter so callers outside the loop (the
// voice HTTP handlers) can fire the same UserPromptSubmit/TaskCompleted
// events text turns do. Nil-safe at the call site.
func (l *Loop) Hooks() HookEmitter {
	if l == nil {
		return nil
	}
	return l.hooks
}

// ToolCatalogBlock renders the dormant-tool catalog string the loop
// prepends to the system prompt before each text turn. Exposed so the
// voice HTTP handler can stamp the same block into a realtime session's
// instructions - the model needs to know the long-tail tool surface
// exists and can be brought online via tool_search → load_tools, exactly
// like in text mode.
//
// The voice path doesn't have a sessionID handy on construction (the
// realtime session is initialised before any turn), so we render the
// catalog without the per-session hidden filter. Acceptable trade-off:
// the catalog over-lists by claude_code__* on Cloud sessions for voice,
// but the schema bundle (live text path) does honor the filter.
func (l *Loop) ToolCatalogBlock(active *tools.ActiveSet) string {
	if l == nil {
		return ""
	}
	return buildToolCatalogBlock(l.tools, active, nil)
}

// SetProjectFetcher wires the active-project resolver (reads mem_sessions and
// derives a short project label from project_path). Set once at boot.
func (l *Loop) SetProjectFetcher(fn func(ctx context.Context, sessionID string) string) {
	l.projectMu.Lock()
	l.projectFetcher = fn
	l.projectMu.Unlock()
}

func (l *Loop) projectFor(ctx context.Context, sessionID string) string {
	l.projectMu.RLock()
	fn := l.projectFetcher
	l.projectMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(ctx, sessionID)
}

// cutReplyText is what a turn that ends EARLY (Stop, budget, provider error)
// keeps as its reply. It is not always the streamed text:
//   - a self-executing brain has already written down everything before
//     brainKept as its own messages, so only the tail is new;
//   - while a self-heal pass is pending, the real answer is preHealText and
//     the pass's buffered preamble is for us, not for him;
//   - while a verify pass is running, the answer he was waiting for is the
//     one before it, with the caveat appended, exactly as the clean end does.
//
// One function for every early exit, so the Stop path and the error path can
// never disagree about what he gets to keep.
func cutReplyText(streamed string, brainRunsOwnTools bool, brainKept int, healPending bool, preHealText, preVerifyText string) string {
	if healPending {
		return strings.TrimSpace(preHealText)
	}
	all := streamed
	if brainRunsOwnTools && brainKept > 0 && brainKept < len(all) {
		all = all[brainKept:]
	}
	partial := strings.TrimSpace(all)
	if strings.TrimSpace(preVerifyText) != "" {
		return strings.TrimSpace(mergeVerifyText(preVerifyText, partial))
	}
	return partial
}

// persistCutReply writes down the reply of a turn that ended early: appended
// to the conversation so the next turn sees it, and filed as TaskCompleted
// {interrupted:true} so the transcript rebuilds it as HIS reply, never as
// narration. extra carries why (timed_out / stalled / errored). Nothing is
// written for an empty reply.
func (l *Loop) persistCutReply(turnID string, s *Session, text string, extra map[string]any) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: text})
	payload := map[string]any{
		"interrupted":   true,
		"message_index": s.replyIndex,
	}
	for k, v := range extra {
		payload[k] = v
	}
	l.fireHookT(turnID, "TaskCompleted", s.ID, s.Project, text, payload)
}

// keepAssistantSegment commits one assistant message the boss saw.
//
// A turn is not one message. It can answer, run a tool and speak again; a
// self-heal, plan-continuation or verify pass takes another swing after the
// first. Only the turn's FINAL text was ever written down (TaskCompleted), so
// every earlier segment lived in his browser and nowhere else: he read a full
// answer, went to Settings to connect LinkedIn, came back, and the answer was
// gone, replaced by whatever the last pass happened to say. The transcript he
// got on reload was never the conversation he had.
//
// So it goes through here: appended to the conversation AND written down, in
// one call, at every site that commits one. A site that forgets to use it is
// the bug coming back, which is why there is nothing else to call.
func (l *Loop) keepAssistantSegment(turnID string, s *Session, text string, calls []llm.ToolCall, meta ...map[string]any) {
	msg := llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: calls}
	// Provider-private carry (a reasoner's own chain of thought, which some
	// vendors want back on the next request). Never persisted, never shown.
	for _, m := range meta {
		if len(m) > 0 {
			msg.Meta = m
		}
	}
	s.Append(msg)
	if strings.TrimSpace(text) == "" {
		return
	}
	l.fireHookT(turnID, "AssistantMessage", s.ID, s.Project, text, map[string]any{
		"interim":       true,
		"message_index": s.replyIndex,
	})
	// The next thing the model says is a new message.
	s.replyIndex++
}

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
		// Defensive - older sessions reattached after a process restart
		// might not have an ActiveSet yet. Backfill with the default.
		s.Active = tools.NewDefaultActiveSet()
	}
	l.mu.Unlock()

	if created {
		// Best-effort hydrate of persisted token counters. We deliberately
		// run this outside l.mu so a slow DB doesn't stall every other
		// session lookup. The lookup is keyed by PK - sub-ms on a healthy
		// pool - but the timeout caps the worst case.
		if store := l.UsageStore(); store != nil && !IsSyntheticSessionID(id) {
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
	Kind      EventKind `json:"kind"`
	SessionID string    `json:"session_id"`
	TextDelta string    `json:"text_delta,omitempty"`
	// MsgIndex, on EventDelta: which assistant message of the turn this text
	// belongs to (Session.replyIndex at the time it streamed). The persisted
	// row for that message carries the same index.
	MsgIndex      int    `json:"msg_index,omitempty"`
	ThinkingDelta string `json:"thinking_delta,omitempty"`
	// ThinkingTokens: a brain that reports how much it is reasoning instead
	// of what it is reasoning (Claude Code redacts the text). See
	// llm.StreamEvent.ThinkingTokens.
	ThinkingTokens int        `json:"thinking_tokens,omitempty"`
	ToolCall       *ToolEvent `json:"tool_call,omitempty"`
	ToolResult     *ToolEvent `json:"tool_result,omitempty"`
	// Set on EventToolInputDelta: the model writing a tool call's arguments
	// live, before the call runs. ToolCallID/ToolName identify the call;
	// InputDelta is the raw partial-JSON chunk. Drives the canvas opening the
	// file and streaming its content as it's generated.
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	InputDelta string          `json:"input_delta,omitempty"`
	Usage      *llm.TokenUsage `json:"usage,omitempty"`
	Error      string          `json:"error,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	// Set on EventEffort (steal C): the per-turn reasoning-effort level chosen
	// and why. Drives the Composer ThinkingChip "Auto · <level>" display. Level
	// "" means the model default (omit) was used.
	EffortLevel  string `json:"effort_level,omitempty"`
	EffortSource string `json:"effort_source,omitempty"`
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
	// in the same tool card - no tab-switch required.
	AwaitingApproval bool   `json:"awaiting_approval,omitempty"`
	ContractID       string `json:"contract_id,omitempty"`
	Preview          string `json:"preview,omitempty"`
}

type EventKind string

const (
	EventDelta          EventKind = "delta"
	EventThinking       EventKind = "thinking"
	EventToolCall       EventKind = "tool_call"
	EventToolInputDelta EventKind = "tool_input_delta"
	EventToolResult     EventKind = "tool_result"
	EventEffort         EventKind = "effort"
	EventComplete       EventKind = "complete"
	EventError          EventKind = "error"
	// EventSteered fires when the loop drains ≥1 mid-turn steer message at an
	// iteration boundary - i.e. the moment the boss's interjection has been
	// absorbed and everything the model says next is a response to it. The ws
	// voice pump uses this to un-squelch speech after a barge-in: the tail of
	// the interrupted reply stays caption-only, the answer to the steer is
	// spoken.
	EventSteered EventKind = "steered"
)

// Run drives one turn of the agent loop. steerCh is optional - when non-nil,
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
func (l *Loop) Run(ctx context.Context, sessionID, userMsg, model string, steerCh <-chan Steer, out chan<- RunEvent) error {
	if l.Provider() == nil {
		return errors.New("agent loop has no LLM provider configured")
	}

	// Carry the boss's request to the Trust gates: when a gated call books an
	// approval card, the card leads with HIS OWN WORDS ("You asked me to: …"),
	// so he can tell what he's approving without reading tool JSON. ctx flows
	// from here to every gate.Authorize call this turn.
	ctx = WithTurnIntent(ctx, userMsg)

	// Central model resolution. An empty model means "use defaults" -
	// honor the boss's active selection from Studio first; only fall
	// through to the provider boot default when no setting exists. This
	// makes every code path that runs the agent (cron, workflow
	// executor, delegate, heartbeat, voice tool turns, ws live chat,
	// resume turns) automatically pick up the active model without
	// having to plumb a settings store through every call site. An
	// explicit non-empty model from the caller still wins - delegate
	// sub-agents that intentionally target a specific id are honored.
	if strings.TrimSpace(model) == "" {
		model = l.resolveActiveModel(ctx)
	}

	// Mark autonomous turns. A nil steer channel means the boss is not
	// actively driving this turn (cron fire, heartbeat scan, delegate/team
	// sub-agent) - only the live chat WS passes a real steer channel. Tools
	// that perform irreversible, boss-owned dispositions (e.g. resolving a
	// follow-up the boss hasn't dealt with) consult tools.IsAutonomous to
	// refuse silent self-service during unattended work. The flag rides ctx
	// into every derived toolCtx below.
	if steerCh == nil {
		ctx = tools.WithAutonomous(ctx)
	}

	// Make the turn cancelable out-of-band so the Stop button on the Agent
	// Work board can abort it. CancelSession(sessionID) fires this cancel; the
	// loop then exits at its next ctx check and bookkeeping closes normally.
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	cancelGen := l.registerRunCancel(sessionID, cancelRun)
	defer l.clearRunCancel(sessionID, cancelGen)

	s := l.GetOrCreateSession(sessionID)
	s.replyIndex = 0

	// A scoped turn (e.g. the locked inbox-triage cron) pre-loads its
	// allowlisted recipe tools as PERMANENT so skills_invoke + the mail/surface
	// tools are directly callable from the first iteration. Without this the
	// brain must load_tools them first and — under TTL decay + the tiny locked
	// catalog — gets stuck re-loading until the loop guard trips, never actually
	// invoking the skill. Wildcards (composio__GMAIL_*) still load on demand.
	if concrete := tools.ScopeConcreteAllow(sessionID); len(concrete) > 0 {
		s.Active.Load(concrete, 0)
	}

	// Tag this turn's observations with the session's ACTIVE project so memory
	// stays per-project coherent when the boss switches projects mid-conversation.
	if p := l.projectFor(ctx, sessionID); p != "" {
		s.Project = p
	}

	// Open a mem_turns row for this turn. The id flows into every
	// fireHook payload below as `turn_id` so capture.go can stamp it on
	// observations; the close call lands on every exit path (success,
	// interrupt, error, iteration cap). turnID empty → recorder unwired
	// → all the threading no-ops cleanly.
	var turnID string
	turnText := userMsg
	if turnText == "" {
		// Resume path: surface the seeded text from the last user message
		// in the session so /logs has something to render in the row.
		if msgs := s.Snapshot(); len(msgs) > 0 {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role == llm.RoleUser && strings.TrimSpace(msgs[i].Content) != "" {
					turnText = msgs[i].Content
					break
				}
			}
		}
	}
	if rec := l.turnRecorder(); rec != nil && !IsSyntheticSessionID(s.ID) {
		if id, err := rec.Open(ctx, s.ID, turnText, model); err == nil {
			turnID = id
		} else {
			log.Printf("turn open: session=%s err=%v", s.ID, err)
		}
	}
	toolCallCount := 0
	// Reactive self-heal trackers: did any tool error this turn, and how many
	// self-heal passes have we already injected. See selfheal.go.
	toolErredThisTurn := false
	selfHealCount := 0
	// One clean retry per turn. See the recovery block in the stream-error
	// path below: a request a brain refuses is rebuilt into the form every
	// brain accepts and sent again, ONCE, before he is ever shown a failure.
	cleanRetryUsed := false
	healedThisTurn := false // a self-heal pass ran this turn; drives the resolved/exhausted hook
	verifyCount := 0        // steal C Lever 3: bounded adversarial-verify passes this turn
	// Plan-continuation backstop trackers: did this turn touch the durable plan
	// (plan_create/plan_update/todo_write), and how many keep-going nudges we've
	// already injected. planTouchedThisTurn gates the nudge so a stale plan from
	// an earlier turn never hijacks an unrelated reply. See plan_continue.go.
	planTouchedThisTurn := false
	planContinueCount := 0
	// Snapshot of the model's substantive reply taken RIGHT BEFORE an
	// adversarial verify pass runs. The verify directive tells the model to be
	// terse and NOT restate the answer, so the post-verify reply is often just a
	// short caveat — which would otherwise REPLACE the whole deliverable in the
	// boss's chat + on reload (the "it only showed a one-line hedge, never the
	// report" complaint). We keep this to merge the two at turn-end.
	var preVerifyText string
	// Per-turn delegate-spawn counter. A runaway model can emit dozens of
	// `delegate` tool calls in a single message (each spawns a sub-agent); this
	// hard-caps spawns per turn so a storm can never happen again.
	delegateSpawns := 0
	// inSelfHealPass flips once the self-heal directive is injected and stays
	// on for the rest of the turn: in an interactive session the loop then
	// refuses source-mutating tool calls (selfheal.go guard).
	inSelfHealPass := false
	// A HEAL PASS IS INTERNAL. The directive that starts it is written by us,
	// not by him, so the reply to it is addressed to us. When the pass then
	// discovers there was nothing to fix, its report ("Nothing broke, boss…")
	// is a conversation between the machine and itself, and it was landing in
	// his chat as the turn's last word - on top of, and then instead of, the
	// real answer he had just read. His words: "why do I want the message
	// about the machine?"
	//
	// So the pass's prose is held back until it has earned a place. If it runs
	// tools, it is doing real work and gets to speak. If it comes back having
	// done nothing, it is dropped and the turn ends on the answer he actually
	// wanted, which is the one that was already on his screen.
	healPending := false
	// healSettled: a pass has already looked and found nothing to fix. The
	// answer it hands back is the one that tripped the detector in the first
	// place, so without this the same words would trigger the same pass again,
	// and he would wait through it twice for a note he is never shown.
	healSettled := false
	preHealText := ""
	var healBuffer strings.Builder

	// An empty userMsg is the "resume" path: run one turn against the
	// already-hydrated session history (e.g. a Discuss-with-Jarvis seeded
	// session whose opening turn is the DashboardSeed context block) without
	// appending a fresh - and empty - user message or firing a bogus
	// UserPromptSubmit hook.
	if userMsg != "" {
		// Files attached to this turn ride the ctx (WithAttachments) and land
		// on the user message as typed blocks the provider ships natively.
		// Their metadata is persisted on the hook payload so the transcript
		// keeps the chips and a post-restart hydrate can reload the files.
		atts := AttachmentsFromContext(ctx)
		s.Append(llm.Message{Role: llm.RoleUser, Content: userMsg, Attachments: atts})
		var payload map[string]any
		if meta := llm.AttachmentsMeta(atts); len(meta) > 0 {
			payload = map[string]any{"attachments": meta}
		}
		// The browser's own id for this message, so the transcript can hand
		// it back and the bubble on screen is matched by id, not by text.
		if cid := turnctx.ClientMessageID(ctx); cid != "" {
			if payload == nil {
				payload = map[string]any{}
			}
			payload["client_id"] = cid
		}
		// A turn INFINITY started (the finish poller waking Jarvis about a
		// stalled build) is filed under its own hook, so the boss never reads
		// a machine brief in his own bubble while the model still has it on
		// rebuild. See sessions.AgentSelfPromptHook.
		hook := "UserPromptSubmit"
		if IsSelfPrompt(ctx) {
			hook = selfPromptHook
		}
		l.fireHookT(turnID, hook, s.ID, s.Project, userMsg, payload)
	}

	// Prompt caching depends on a byte-identical prefix across a session's
	// turns, so we split the system prompt into a STABLE segment (the soul/
	// base - the only thing that doesn't change turn to turn) and a VOLATILE
	// segment (RRF retrieval, current_time, tool catalog, account overlay,
	// voice/wind-down). The stable segment leads and carries the cache
	// breakpoint; the volatile segment follows so it never invalidates the
	// cached prefix. Reversing the old prepend chain into an appended volatile
	// builder is the whole fix (see provider.SystemPrompt / CachingProvider).
	stableSystem := l.systemPrompt
	if override := strings.TrimSpace(s.SystemPromptOverride); override != "" {
		stableSystem = override
	}
	var volatile strings.Builder
	appendVolatile := func(block string) {
		block = strings.TrimSpace(block)
		if block == "" {
			return
		}
		if volatile.Len() > 0 {
			volatile.WriteString("\n\n")
		}
		volatile.WriteString(block)
	}
	if l.memory != nil {
		if prefix, err := l.memory.BuildSystemPrefix(ctx, s.ID, userMsg); err == nil {
			appendVolatile(prefix)
		}
	}
	if l.skills != nil {
		appendVolatile(l.skills.MatchAndPrefix(userMsg, 5))
	}
	// Snapshot the per-session hidden-tools set once per turn - used to
	// drop tools the bridge layer (or any other policy) has decided the
	// model must not see this turn. Today: hides `claude_code__*` on
	// Cloud-routed sessions so the model can't accidentally edit the
	// Mac filesystem when working in the Cloud workspace.
	hidden := l.hiddenForSession(ctx, s.ID)
	// The dormant tool catalog so the model knows what exists even when
	// it doesn't have the schema in hand; it unlocks the tool_search →
	// load_tools loop. Lifted into world state below (sent once, then only
	// on change). Skipped for a brain that runs its own tools: Claude Code
	// sees Infinity's registry as mcp__infinity__* through its own deferred
	// list, and 28K chars of names it cannot call under those names was
	// the single largest block in every prompt it got.
	if !llm.SelfExecuting(l.Provider()) {
		appendVolatile(buildToolCatalogBlock(l.tools, s.Active, hidden))
	}
	// Prepend the connected-accounts overlay so the model can route to
	// the right OAuth account when a tool has multi-account support
	// (e.g. four Gmail mailboxes). The block lists per-toolkit
	// alias → account_id mappings; the model picks based on the user's
	// stated intent.
	if l.accounts != nil {
		appendVolatile(l.accounts.SystemPromptBlock())
	}
	// Voice turns get a thin delivery overlay. Per-turn via ctx, not a Session
	// field, because text + voice share the session. Capability is unchanged -
	// this only shapes how the same brain talks when its words are spoken aloud.
	if VoiceModeFromContext(ctx) {
		appendVolatile(voiceModeSystemOverlay)
	}
	// World state vs this turn's context (see worldstate.go). The stable
	// overlays (tool catalog, connected accounts, bridge, CLI tools, compass,
	// goals) are lifted out and sent ONCE per session as a message that stays
	// in the history, then only as a diff when they change; Codex does the
	// same with its environment context. What remains is genuinely per-turn
	// (retrieval, current time, plan, lessons, reflexes) and is PINNED to the
	// user message that opened the turn (session_context.go), so it sits at
	// one byte offset for every call of the turn. The discuss overlay is
	// pinned with it: it is a register for the turn, not for one call.
	perTurnContext, worldSections, _ := splitWorldSections(volatile.String())
	if ws := worldStateMessage(&s.world, worldSections); ws != "" {
		s.insertWorldState(ws)
	}
	if overlay := discussOverlayFor(ctx, true); overlay != "" {
		perTurnContext = joinBlocks(perTurnContext, overlay)
	}
	s.pinVolatile(perTurnContext)
	defer s.clearVolatile()
	infoLog.Printf("turn-context: session=%s pinned=%d chars world_sections=%d", s.ID, len(perTurnContext), len(worldSections))
	// Bytes of files the model has already seen stop riding every call.
	if n := s.degradeOldAttachments(); n > 0 {
		infoLog.Printf("turn-context: session=%s degraded %d earlier attachment(s) to text", s.ID, n)
	}
	// TTL'd tools age once per TURN. Aging them before every LLM call (as
	// this used to) changed the tool array mid-turn, which invalidates
	// tools + system + history on every vendor.
	s.Active.DecayTTL()
	// What is left for the per-call system overlay: nothing, unless a
	// wind-down notice cannot ride the newest tool result.
	volatileSystem := ""

	// Per-turn continuation state. A turn runs in "segments": each segment is
	// up to maxToolIterations tool-loop iterations. When a segment exhausts its
	// budget mid-work we DON'T hard-error and abandon a half-done plan — we
	// checkpoint (compact the context; the plan/todo is already durable) and run
	// another segment, but only while the model is still making progress and
	// only up to maxTurnSegments. So a long-but-legit "diagnose → build → test →
	// commit → push" job finishes across segments, while a genuinely stuck one
	// still stops. windDownAt is ~80% of the segment budget: past it the system
	// prompt gains turnWindDownBlock so the model lands its step cleanly.
	segment := 0
	segmentSuccesses := 0
	windDownAt := l.maxToolIterations - l.maxToolIterations/5

	// steal C: size the per-turn reasoning effort ONCE, from the session's live
	// signals (resolver wired in serve.go). Same model, variable compute.
	// perTurnEffort rides streamCtx into every iteration's LLM call below; "" =
	// omit (model default), so an un-escalated turn costs exactly what it did
	// before C existed. Surfaced live as EventEffort so the Composer can show
	// "Auto · <level>".
	perTurnEffort, effortSource := l.resolveEffort(ctx, EffortRequest{
		SessionID: s.ID,
		Model:     model,
		Project:   s.Project,
		Pinned:    EffortPinFromContext(ctx),
	})
	if perTurnEffort != "" || effortSource != "" {
		emit(out, RunEvent{Kind: EventEffort, SessionID: s.ID, EffortLevel: string(perTurnEffort), EffortSource: effortSource})
	}

	for iter := 0; ; iter++ {
		if iter >= l.maxToolIterations {
			// Segment budget exhausted. Continue into a fresh segment only with
			// segment headroom AND ≥1 successful tool result this segment — a
			// whole segment with zero successes is thrash, not progress.
			if segment+1 >= l.maxTurnSegments || segmentSuccesses == 0 {
				break
			}
			compacted := l.compactTurnNow(ctx, s, l.Provider(), stableSystem,
				l.tools.DefinitionsFor(filterToolNames(s.Active.Names(), l.hiddenForSession(ctx, s.ID))))
			segment++
			segmentSuccesses = 0
			iter = 0
			note := fmt.Sprintf("Hit this turn's tool budget after %d calls — checkpointing and continuing (round %d/%d).",
				toolCallCount, segment+1, l.maxTurnSegments)
			if compacted {
				note += " Compacted the context to stay sharp."
			}
			emit(out, RunEvent{Kind: EventThinking, SessionID: s.ID, ThinkingDelta: note + "\n"})
			infoLog.Printf("turn-continuation: session=%s segment=%d/%d toolcalls=%d compacted=%v",
				s.ID, segment+1, l.maxTurnSegments, toolCallCount, compacted)
			// fall through to run the new segment's first iteration
		}
		select {
		case <-ctx.Done():
			emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, StopReason: "interrupted"})
			l.closeTurn(context.Background(), turnID, TurnCloseFields{
				StopReason:    "interrupted",
				Status:        "interrupted",
				ToolCallCount: toolCallCount,
				Summary:       summarizeReply("", toolCallCount),
			})
			return nil
		default:
		}

		// Drain steered messages so the next LLM call sees them as fresh user
		// input. Each drained message is persisted via the UserPromptSubmit
		// hook (with steered=true payload) so transcript reload renders them
		// in order with the rest of the conversation. EventSteered tells the
		// ws voice pump the steer is absorbed (see EventKind doc).
		if l.drainSteer(steerCh, s) > 0 {
			emit(out, RunEvent{Kind: EventSteered, SessionID: s.ID})
		}

		llmEvents := make(chan llm.StreamEvent, 64)
		var resp llm.Response
		var streamErr error
		streamDone := make(chan struct{})

		// Snapshot the provider once per iteration - a Settings PUT that
		// swaps mid-turn will affect the *next* iteration, not this one,
		// keeping the in-flight stream coherent.
		provider := l.Provider()
		// Only ship schemas for tools currently in the session's active
		// set - the dormant long tail lives in the system-prompt catalog
		// block and surfaces via tool_search. This is the core Phase-1
		// context-budget win.
		//
		// Apply the per-turn visibility filter so e.g. claude_code__*
		// schemas don't reach the model when the session is routed to
		// Cloud. Refreshed each iteration because the bridge can flip
		// mid-session (Mac comes back online, boss flips preference).
		visibleNames := filterToolNames(s.Active.Names(), l.hiddenForSession(ctx, s.ID))
		toolDefs := l.tools.DefinitionsFor(visibleNames)
		// Once this segment crosses ~80% of its tool budget, overlay the
		// wind-down notice so the model lands its current step and checkpoints
		// before the cap, rather than getting cut off mid-action. Ephemeral:
		// applied to this call only, never persisted onto the session. It is
		// volatile, so it lands AFTER the cached stable prefix.
		sysVolatile := volatileSystem
		// Wind-down rides the newest tool result (new content anyway, so the
		// cached prefix does not move); only when there is none does it fall
		// back to the per-call system overlay.
		if iter >= windDownAt && !s.appendWindDown() {
			sysVolatile = joinBlocks(sysVolatile, turnWindDownBlock)
		}
		// Context economy before the call: clear old tool results, then
		// compact, both gated on the measured window fill of the previous
		// call. See context_maint.go.
		l.maintainContext(ctx, s, provider, model, stableSystem, toolDefs)
		sys := llm.SystemPrompt{Stable: stableSystem, Volatile: sysVolatile}
		// Stamp the session id so OpenAI/OAuth forward it as prompt_cache_key,
		// the steal-C per-turn effort so each provider sizes reasoning inside
		// its own gate (WithEffort is a no-op when perTurnEffort is ""), and
		// the segment's tool budget as the cap on a self-executing brain's own
		// loop (--max-turns).
		streamCtx := llm.WithMaxTurns(llm.WithEffort(llm.WithCacheKey(ctx, s.ID), perTurnEffort), l.maxToolIterations)
		go func() {
			defer close(streamDone)
			if cp, ok := provider.(llm.CachingProvider); ok {
				resp, streamErr = cp.StreamCached(streamCtx, model, sys, s.Snapshot(), toolDefs, llmEvents)
			} else {
				resp, streamErr = provider.Stream(streamCtx, model, sys.Render(), s.Snapshot(), toolDefs, llmEvents)
			}
			close(llmEvents)
		}()

		var partialText strings.Builder
		// A failure the STREAM reported, held rather than shown.
		//
		// It used to go straight to his screen the instant it arrived, before
		// anyone knew how the turn would end - and this loop recovers from
		// most of them (the clean retry below strips the request back and asks
		// again). So he watched an error appear and then remove itself on the
		// next refetch, because a turn that recovers closes `ok` and no
		// durable error row is ever written. An error he sees must be one that
		// actually happened.
		//
		// Same trade llm/failover.go already makes: hold the error until the
		// outcome is known, so a failover nobody needed to hear about is
		// silent. Below, it either becomes the turn's real failure (handled by
		// the one existing error path) or it is dropped.
		var streamedErr string
		// Does this brain run its own tools? Asked once per iteration because
		// the provider can be hot-swapped mid-conversation.
		//
		// Asked through llm.SelfExecuting, which unwraps the decorators, and
		// NEVER as a type assertion on `provider`: what the loop is handed is
		// always a noDashesProvider (factory.go wraps every registration),
		// and a direct assertion on it was false for every Claude Max turn
		// the boss ever had. The whole tool-row branch below sat dead behind
		// it. See the helper's comment for what that cost.
		brainRunsOwnTools := llm.SelfExecuting(provider)
		// Calls the brain made that have not reported back yet, so the result
		// can be paired with the name and input the boss already saw. Anything
		// still in here when the stream ends never returned.
		brainCalls := map[string]llm.ToolCall{}
		// How much of partialText has already been written down as a finished
		// assistant message. See commitBrainSegment.
		brainKept := 0
		// commitBrainSegment writes down what the boss has ALREADY READ, the
		// moment it stops changing, instead of at the end of the turn.
		//
		// A brain that runs its own tools answers one of our turns in several
		// messages with tool work between them, and only the LAST of those
		// used to reach the database: keepAssistantSegment's three call sites
		// are all gated on resp.ToolCalls being non-empty, which never happens
		// for this kind of brain. So everything before the final message lived
		// in his browser and nowhere else. He read a full answer, navigated
		// away, came back, and it was gone - "messages removing from the
		// stream", 2026-09-01.
		//
		// A tool call is the boundary: the model finished saying something and
		// went to do something. keepAssistantSegment is the only writer, per
		// its own doc, so this is a new CALL SITE and not a second path.
		commitBrainSegment := func() {
			if !brainRunsOwnTools {
				return
			}
			all := partialText.String()
			if brainKept > len(all) {
				brainKept = len(all)
			}
			seg := all[brainKept:]
			brainKept = len(all)
			if strings.TrimSpace(seg) == "" {
				return
			}
			l.keepAssistantSegment(turnID, s, seg, nil)
		}
		for ev := range llmEvents {
			switch ev.Kind {
			case llm.StreamText:
				partialText.WriteString(ev.TextDelta)
				if healPending {
					// Held until the pass shows whether it is doing anything.
					// Flushed the moment it calls a tool; dropped if it comes
					// back empty-handed. See healPending's declaration.
					healBuffer.WriteString(ev.TextDelta)
					continue
				}
				emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: ev.TextDelta, MsgIndex: s.replyIndex})
			case llm.StreamThinking:
				emit(out, RunEvent{
					Kind:           EventThinking,
					SessionID:      s.ID,
					ThinkingDelta:  ev.ThinkingDelta,
					ThinkingTokens: ev.ThinkingTokens,
				})
			case llm.StreamToolInputDelta:
				// Forward the live tool-argument chunk so the canvas can open
				// the file and type it in as the model writes it. Best-effort
				// preview — the subsequent EventToolCall carries authoritative
				// full input. Purely additive; tool execution still runs off
				// resp.ToolCalls after the stream completes.
				emit(out, RunEvent{
					Kind:       EventToolInputDelta,
					SessionID:  s.ID,
					ToolCallID: ev.ToolCallID,
					ToolName:   ev.ToolName,
					InputDelta: ev.InputDelta,
				})
			case llm.StreamToolCall:
				// A brain that runs its OWN tools (Claude Code) executed this
				// inside its harness: it is not coming back as a tool call for
				// the executor below, so this event IS the record of it. Emit
				// and capture IN ORDER, as it streams, so the step lands in
				// his ledger where it happened - between the words either side
				// of it, never after the reply.
				//
				// Other providers stream the same event and their calls DO
				// come back to be executed, so theirs are handled there and
				// ignored here.
				if ev.ToolCall != nil && brainRunsOwnTools {
					// Whatever he said before reaching for this tool is
					// finished. Write it down now, not at the end of the turn.
					commitBrainSegment()
					brainCalls[ev.ToolCall.ID] = *ev.ToolCall
					emit(out, RunEvent{Kind: EventToolCall, SessionID: s.ID, ToolCall: &ToolEvent{
						ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Input: ev.ToolCall.Input,
						StartedAt: time.Now().UTC(),
					}})
					// And write down the CALL, not only the result.
					//
					// Only results were persisted, so a five-minute command
					// was invisible to a reload for five minutes: the boss
					// refreshed mid-turn and got his own message back with
					// nothing under it, which is indistinguishable from a
					// brain that never started. The row carries no output yet;
					// the result below files the same tool_call_id and the
					// transcript keeps the completed one.
					l.fireHookT(turnID, "PostToolUse", s.ID, s.Project,
						ev.ToolCall.Name+" (running)", map[string]any{
							"name":         ev.ToolCall.Name,
							"input":        ev.ToolCall.Input,
							"tool_call_id": ev.ToolCall.ID,
							"executed_by":  provider.Name(),
							"running":      true,
						})
				}
			case llm.StreamToolResult:
				// The other half. Every other brain hands our loop the call
				// and the loop produces the result, so both halves are ours by
				// construction; a harness runs both inside its own session.
				// Recording the result here is what makes a turn on this brain
				// read - and remember - like a turn on any other: the row
				// completes, and memory holds what came back rather than only
				// what was attempted.
				if brainRunsOwnTools && ev.ToolCallID != "" {
					call := brainCalls[ev.ToolCallID]
					name := strings.TrimSpace(ev.ToolName)
					if name == "" {
						name = call.Name
					}
					delete(brainCalls, ev.ToolCallID)
					emit(out, RunEvent{Kind: EventToolResult, SessionID: s.ID, ToolResult: &ToolEvent{
						ID: ev.ToolCallID, Name: name, Input: call.Input,
						Output: ev.ToolOutput, IsError: ev.ToolError,
						StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(),
					}})
					hook := "PostToolUse"
					if ev.ToolError {
						hook = "PostToolUseFailure"
					}
					l.fireHookT(turnID, hook, s.ID, s.Project, name+": "+ev.ToolOutput, map[string]any{
						"name":         name,
						"input":        call.Input,
						"output":       ev.ToolOutput,
						"tool_call_id": ev.ToolCallID,
						"executed_by":  provider.Name(),
					})
				}
			case llm.StreamNotice:
				// Provider-layer heads-up for the boss (brain failover on a
				// spent plan, back on the primary). Same shape as the bridge
				// failover notice: one italic line in the reply stream.
				emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: ev.TextDelta, MsgIndex: s.replyIndex})
			case llm.StreamError:
				streamedErr = ev.Err
			}
		}

		<-streamDone

		// A stream that reported a failure, returned no error of its own, and
		// produced nothing to show is a failure however the provider filed it.
		// Promote it so the one error path below owns it - status errored, a
		// durable row, and a card that is still there when he comes back.
		// Anything the loop went on to recover from stays silent, which is the
		// whole point of holding it.
		if streamErr == nil && streamedErr != "" && strings.TrimSpace(partialText.String()) == "" {
			streamErr = errors.New(streamedErr)
		}

		// DID THE HEAL PASS DO ANYTHING?
		//
		// Tool calls mean it is working: the prose it wrote is a preamble to
		// real work, so it is released and the answer that preceded the pass
		// becomes a message in its own right. No tool calls mean it looked and
		// found nothing to fix, so its report is for us, not for him: it is
		// dropped, and the turn ends on the answer he already had.
		if healPending && len(resp.ToolCalls) > 0 {
			healPending = false
			l.keepAssistantSegment(turnID, s, preHealText, nil)
			if held := healBuffer.String(); strings.TrimSpace(held) != "" {
				emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: held, MsgIndex: s.replyIndex})
			}
			healBuffer.Reset()
			preHealText = ""
		} else if healPending && streamErr == nil {
			healPending = false
			healSettled = true
			healBuffer.Reset()
			if strings.TrimSpace(preHealText) != "" {
				// The turn ends on what he was actually waiting for.
				resp.Text = preHealText
			}
			preHealText = ""
		}

		// A call with no result by the end of the stream never came back: the
		// turn was cut off, or the line carrying its result was too big to
		// read. Record it as attempted rather than dropping it, so memory
		// never quietly loses a step that happened.
		for id, call := range brainCalls {
			l.fireHookT(turnID, "PostToolUse", s.ID, s.Project,
				call.Name+" (run by the brain; no result came back)", map[string]any{
					"name":         call.Name,
					"input":        call.Input,
					"tool_call_id": id,
					"executed_by":  provider.Name(),
					"no_result":    true,
				})
		}

		// Record real API-reported usage on every successful stream. The
		// context meter reads s.lastInputTokens to show current window
		// fill - 0 on empty sessions, accurate after each turn.
		s.RecordUsage(resp.Usage, provider.Model())
		// One line per call so a cache break is visible where it happens
		// (Claude Code: "alert on cache breaks and treat them as
		// incidents"). uncached is what was re-written at full price.
		if u := resp.Usage; u.PromptTokens() > 0 {
			infoLog.Printf("llm-call: session=%s iter=%d model=%s prompt=%d cached=%d cache_write=%d uncached=%d out=%d tools=%d effort=%s",
				s.ID, iter, provider.Model(), u.PromptTokens(), u.CacheRead, u.CacheWrite, u.Input, u.Output, len(toolDefs), perTurnEffort)
		}
		// Persist counters so a process restart doesn't reset the meter
		// to 0% on a session with real history. Best-effort + detached
		// context so the user-visible turn isn't gated on the DB write.
		if store := l.UsageStore(); store != nil && !IsSyntheticSessionID(s.ID) && (resp.Usage.Input > 0 || resp.Usage.Output > 0 || resp.Usage.CacheRead > 0) {
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
		l.recordLLMCost(provider, model, resp.Usage)

		if streamErr != nil {
			// A canceled/expired TURN CONTEXT is not a provider failure. It fires
			// on the Stop button (context.Canceled) and on the per-turn wall-clock
			// budget (context.DeadlineExceeded) — the latter used to fall through
			// to the error path and mark the turn 'errored' with a raw "context
			// deadline exceeded" that ALSO never fired TaskCompleted, so the chat
			// RELOAD (rebuilt from UserPromptSubmit + TaskCompleted) showed NOTHING
			// on the assistant side. That is the boss's "it never even showed
			// anything / I never saw an error persisted, and it just sits there"
			// complaint. Handle BOTH here: fire TaskCompleted so the partial turn
			// persists + renders on reload, close cleanly as interrupted (not
			// errored), and on a budget timeout append a resumable note (the plan
			// is durable; the deployed plan-continuation backstop resumes it when
			// he says "continue").
			if ctx.Err() != nil {
				partial := cutReplyText(partialText.String(), brainRunsOwnTools, brainKept, healPending, preHealText, preVerifyText)
				// Which budget ended it, if it was a budget and not the boss.
				// The server cancels with a cause (server/turn_budget.go); a
				// plain cancel is the Stop button.
				stalled, ceiling := TurnBudgetCause(ctx)
				timedOut := stalled || ceiling
				stopReason := "interrupted"
				switch {
				case stalled:
					stopReason = "stalled"
					note := "\n\n_(The model went quiet on me for too long, so I stopped waiting. Everything I set up, including the plan, is saved. Say 'continue' and I'll pick up where I left off.)_"
					emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: note, MsgIndex: s.replyIndex})
					partial = strings.TrimSpace(partial + note)
				case ceiling:
					stopReason = "time_budget"
					note := "\n\n_(I hit my time budget for this one turn. Everything I set up, including the plan, is saved. Say 'continue' and I'll pick up where I left off.)_"
					emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: note, MsgIndex: s.replyIndex})
					partial = strings.TrimSpace(partial + note)
				}
				l.persistCutReply(turnID, s, partial, map[string]any{
					"timed_out": timedOut,
					"stalled":   stalled,
				})
				// The wire says "interrupted" for every cut turn: the browser
				// marks the bubble and settles; the precise reason is on the
				// mem_turns row for /logs.
				emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, StopReason: "interrupted"})
				l.closeTurn(context.Background(), turnID, TurnCloseFields{
					AssistantText:    partial,
					StopReason:       stopReason,
					Status:           "interrupted",
					InputTokens:      resp.Usage.PromptTokens(),
					OutputTokens:     resp.Usage.Output,
					CacheReadTokens:  resp.Usage.CacheRead,
					CacheWriteTokens: resp.Usage.CacheWrite,
					ToolCallCount:    toolCallCount,
					Summary:          summarizeReply(partial, toolCallCount),
				})
				// Settle the foreground plan so a step left in_progress at the
				// budget/interrupt boundary is closed cleanly, not reaper-failed.
				l.settlePlanOnTurnEnd(s.ID)
				// A turn that did real work then hit its budget still deserves a
				// session title — the namer otherwise only runs on the clean-end
				// path, so a long-but-timed-out session stayed nameless (part of
				// the "naming seems broken" report). Best-effort, async, cheap.
				if l.namer != nil {
					l.namer.MaybeName(s.ID, turnText, partial)
				}
				return nil
			}
			// Credential failure (revoked OAuth token, invalid API key)? Don't
			// dump a raw 401 into chat and abandon the request. PARK the turn,
			// tell the boss in-conversation how to reconnect, and let the reauth
			// poller replay it the moment the brain is healthy again (he re-auths
			// OR switches models). Provider-agnostic. Falls through to the normal
			// error path if there's nothing to replay or no parker wired.
			if parker := l.reauthParker(); parker != nil &&
				strings.TrimSpace(turnText) != "" &&
				llm.IsAuthFailure(streamErr.Error()) {
				provName := provider.Name()
				reason := streamErr.Error()
				if perr := parker.Park(context.Background(), s.ID, provName, model, turnText, reason); perr == nil {
					emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, StopReason: "awaiting_reauth"})
					l.closeTurn(context.Background(), turnID, TurnCloseFields{
						StopReason:       "awaiting_reauth",
						Status:           "awaiting_reauth",
						InputTokens:      resp.Usage.PromptTokens(),
						OutputTokens:     resp.Usage.Output,
						CacheReadTokens:  resp.Usage.CacheRead,
						CacheWriteTokens: resp.Usage.CacheWrite,
						ToolCallCount:    toolCallCount,
						Error:            reason,
						Summary:          "paused — model needs re-auth; will auto-resume",
					})
					return nil
				}
				// Park failed → fall through to the normal error path below.
			}
			// LAST CHANCE BEFORE HE SEES A FAILURE.
			//
			// Almost every way a turn has died in practice was the brain
			// refusing the SHAPE of what we sent, never the question: a PDF
			// block one vendor takes and the next rejects, a tool call left
			// unanswered by an earlier crash that poisons every turn after
			// it, a handle to a session that no longer exists. Those are our
			// problem, and he should never be the one to notice them.
			//
			// So the rule is not a list of vendor sentences to recognise.
			// It is: strip the request back to what every brain accepts and
			// ask the same question again, once. If that also fails, the
			// failure is real and he gets told plainly.
			//
			// Gated on nothing having streamed yet: once he is reading an
			// answer, restarting it would rewind the reply under him.
			if !cleanRetryUsed && partialText.Len() == 0 && !llm.IsUnrecoverable(streamErr) &&
				!llm.IsAuthFailure(streamErr.Error()) {
				if safe, changed := llm.MakeSafe(s.Snapshot()); changed {
					cleanRetryUsed = true
					s.ReplaceMessages(safe)
					infoLog.Printf("recovery: %v — retrying the same turn with a request every brain accepts", streamErr)
					continue
				}
			}
			// Whatever he was reading when the provider failed is kept, the
			// same way a Stop keeps it. It used to be dropped here: the only
			// persisted assistant rows of an errored turn were the interim
			// segments, so a reply cut by a usage cap vanished on reload.
			partial := cutReplyText(partialText.String(), brainRunsOwnTools, brainKept, healPending, preHealText, preVerifyText)
			l.persistCutReply(turnID, s, partial, map[string]any{"errored": true})
			emitHumanError(out, s.ID, streamErr.Error())
			l.closeTurn(context.Background(), turnID, TurnCloseFields{
				AssistantText:    partial,
				StopReason:       "error",
				Status:           "errored",
				InputTokens:      resp.Usage.PromptTokens(),
				OutputTokens:     resp.Usage.Output,
				CacheReadTokens:  resp.Usage.CacheRead,
				CacheWriteTokens: resp.Usage.CacheWrite,
				ToolCallCount:    toolCallCount,
				Error:            streamErr.Error(),
				Summary:          summarizeReply(partial, toolCallCount),
			})
			if partial != "" && l.namer != nil {
				l.namer.MaybeName(s.ID, turnText, partial)
			}
			return streamErr
		}

		if len(resp.ToolCalls) == 0 {
			// REACTIVE SELF-HEAL: the model is about to end the turn with no
			// further tool calls. If it's handing back an UNRESOLVED failure
			// (and we haven't already nudged this turn), don't let it punt —
			// keep its draft, inject the self-heal directive, and run another
			// pass so it investigates + fixes + verifies with its own tools.
			// Capped by maxSelfHealPerTurn so a genuinely-stuck turn still ends.
			// A conversation cannot fail to deliver: while the boss is talking it
			// through, none of the finishing reflexes (self-heal, plan-continue,
			// verify) run. See consent.go turnIsDiscuss.
			discussing := turnIsDiscuss(ctx)
			if !discussing && !healSettled && selfHealCount < maxSelfHealPerTurn && shouldSelfHeal(resp.Text, toolErredThisTurn) {
				selfHealCount++
				healedThisTurn = true
				inSelfHealPass = true
				// Held, not written down: whether this counts as a message of
				// its own depends on what the pass does next. If the pass does
				// nothing, THIS is the turn's final answer, not a segment
				// before one.
				healPending = true
				preHealText = resp.Text
				healBuffer.Reset()
				s.Append(llm.Message{Role: llm.RoleAssistant, Content: resp.Text})
				s.Append(llm.Message{Role: llm.RoleUser, Content: selfHealDirective})
				toolErredThisTurn = false // fresh slate for the heal pass
				continue
			}

			// PLAN-CONTINUATION BACKSTOP: the model is ending the turn cleanly,
			// but it drafted/advanced a plan THIS turn and that plan still has
			// unfinished steps. That's the "made a plan, then quit" pathology —
			// don't let it stop after only laying out the work. Keep its draft,
			// inject the keep-going directive, and run another pass so it actually
			// EXECUTES the plan. Gated on planTouchedThisTurn (a stale plan from an
			// earlier turn can't hijack an unrelated reply), skipped while a
			// failure is in play (self-heal owns that), and bounded per turn so a
			// genuinely-blocked plan still ends. See plan_continue.go.
			if !discussing && planContinueCount < maxPlanContinuePerTurn && planTouchedThisTurn &&
				!shouldSelfHeal(resp.Text, toolErredThisTurn) &&
				l.hasUnfinishedPlan(ctx, s.ID) {
				planContinueCount++
				l.keepAssistantSegment(turnID, s, resp.Text, nil)
				s.Append(llm.Message{Role: llm.RoleUser, Content: planContinueDirective})
				emit(out, RunEvent{Kind: EventThinking, SessionID: s.ID, ThinkingDelta: "Plan still has open steps — continuing instead of stopping.\n"})
				continue
			}

			// STEAL C — LEVER 3: adversarial-verify pass. On the hardest turns
			// (effort resolved to high/xhigh) that are ending cleanly, run ONE
			// bounded pass where the model red-teams its own answer before the
			// turn ends. Never stacks on a self-heal pass (!healedThisTurn). The
			// directive is DATA (verifyDirectiveText, seeded in infinity_meta) —
			// Rule #1b — so empty directive / migration-not-applied = inert. This
			// mirrors the self-heal mechanic exactly: it's one extra iteration
			// INSIDE this already-tracked turn, so it rides the turn's hooks +
			// emits an EventEffort{verify_pass} signal rather than booking a
			// separate mem_runs row.
			if !discussing && verifyCount < maxVerifyPerTurn && !healedThisTurn && !verifyDisabled() &&
				(perTurnEffort == llm.EffortHigh || perTurnEffort == llm.EffortXHigh) &&
				shouldVerify(resp.Text) {
				if directive := l.verifyDirectiveText(); directive != "" {
					verifyCount++
					// Remember the deliverable so a terse post-verify caveat can't
					// erase it (issue: the boss got "the weak spot is the citation
					// layer…" INSTEAD of his report summary).
					preVerifyText = resp.Text
					// NOT keepAssistantSegment: the verify pass carries this text
					// forward itself. mergeVerifyText appends the caveat to THIS
					// answer and persists the pair as the turn's final text, so
					// writing it down here too would show him the answer twice.
					s.Append(llm.Message{Role: llm.RoleAssistant, Content: resp.Text})
					s.Append(llm.Message{Role: llm.RoleUser, Content: directive})
					emit(out, RunEvent{Kind: EventEffort, SessionID: s.ID, EffortLevel: string(perTurnEffort), EffortSource: "verify_pass"})
					continue
				}
			}

			// A self-heal pass ran this turn and we're now ending it. Fire the
			// structural hook so the receipt + durable guard get encoded by the
			// self-heal encoder — guaranteed by code, never dependent on the
			// model "remembering" to write a memory (Rule #1b). Resolved when the
			// final reply no longer reads as an unresolved failure; exhausted when
			// the heal budget ran out and it still does.
			if healedThisTurn {
				healEvent := "SelfHealResolved"
				if shouldSelfHeal(resp.Text, toolErredThisTurn) {
					healEvent = "SelfHealExhausted"
				}
				l.fireHookT(turnID, healEvent, s.ID, s.Project, resp.Text, map[string]any{
					"heal_passes": selfHealCount,
					"resolved":    healEvent == "SelfHealResolved",
				})
			}

			// VERIFY-PASS MERGE (Rule #1b mechanic): if an adversarial verify
			// pass ran, the model was told to be terse and NOT restate the
			// answer, so resp.Text is often a bare caveat. Persisting that alone
			// makes the chat RELOAD (rebuilt from TaskCompleted) show only the
			// hedge — the boss's "it never told me it was done, just a one-liner"
			// complaint. Keep the deliverable: a substantial post-verify reply is
			// a genuine rewrite (use it); a much-shorter one is a caveat (show the
			// original answer with the caveat appended).
			finalText := mergeVerifyText(preVerifyText, resp.Text)
			s.Append(llm.Message{Role: llm.RoleAssistant, Content: finalText})
			emit(out, RunEvent{Kind: EventComplete, SessionID: s.ID, Usage: &resp.Usage, StopReason: resp.StopReason})
			l.fireHookT(turnID, "TaskCompleted", s.ID, s.Project, finalText, map[string]any{
				"input_tokens":  resp.Usage.PromptTokens(),
				"output_tokens": resp.Usage.Output,
				"message_index": s.replyIndex,
			})
			// Status reflects what the boss sees: empty reply with no tool
			// work is a confused decode; empty reply with tool work is a
			// "did stuff but didn't summarise" path; non-empty is ok.
			status := "ok"
			if strings.TrimSpace(finalText) == "" {
				status = "empty"
			}
			l.closeTurn(context.Background(), turnID, TurnCloseFields{
				AssistantText:    finalText,
				StopReason:       resp.StopReason,
				Status:           status,
				InputTokens:      resp.Usage.PromptTokens(),
				OutputTokens:     resp.Usage.Output,
				CacheReadTokens:  resp.Usage.CacheRead,
				CacheWriteTokens: resp.Usage.CacheWrite,
				ToolCallCount:    toolCallCount,
				Summary:          summarizeReply(finalText, toolCallCount),
			})
			// Settle the foreground plan the instant the turn ends, so an
			// in_progress step is closed cleanly ('skipped'/'done') instead of
			// dead-spinning until the reaper FALSELY fails it. FinalizeSession is
			// idempotent + detached-context. See plan_continue.go.
			l.settlePlanOnTurnEnd(s.ID)
			// Auto-name the session after the first complete exchange.
			// MaybeName is cheap when the session is already named (one
			// indexed lookup); it drafts a title async only when we need a
			// fresh one. Safe to call on every turn. Use turnText, not
			// userMsg: on the resume path (Discuss-with-Jarvis seeded
			// sessions) userMsg is empty and turnText carries the seeded
			// opening message, so those sessions get a title too instead of
			// staying nameless in the sessions drawer.
			if l.namer != nil {
				l.namer.MaybeName(s.ID, turnText, finalText)
			}
			// Auto-compaction: if this turn's reported input crossed the
			// threshold, run compaction in the background so the *next*
			// turn lands on a tighter buffer. We don't block the return
			// because the user's response has already streamed.
			l.maybeAutoCompact(s, resp.Usage.WindowTokens())
			return nil
		}

		l.keepAssistantSegment(turnID, s, resp.Text, resp.ToolCalls, resp.Meta)

		for _, tc := range resp.ToolCalls {
			startedAt := time.Now().UTC()
			toolCallCount++

			// Mark that this turn is doing plan work, so a premature stop with an
			// unfinished plan triggers the keep-going nudge (plan_continue.go).
			if isPlanTool(tc.Name) {
				planTouchedThisTurn = true
			}

			// Delegate fan-out cap. Spawning a sub-agent is expensive and a
			// confused model can fire dozens in one turn (the "100 spinning
			// delegates" storm). Past the cap, refuse and tell the model to do
			// the work itself — delegation is for genuinely parallel,
			// independent sub-tasks, not for work it can do directly.
			if tc.Name == "delegate" || tc.Name == "delegate_parallel" {
				delegateSpawns++
				if delegateSpawns > maxDelegateSpawnsPerTurn {
					capMsg := fmt.Sprintf(
						"BLOCKED: delegate cap reached (%d this turn, max %d). Stop delegating — do this task DIRECTLY with your own tools (project_create, fs_save, document_create, etc.). Delegation is only for a few genuinely parallel, independent sub-tasks.",
						delegateSpawns, maxDelegateSpawnsPerTurn)
					emit(out, RunEvent{Kind: EventToolCall, SessionID: s.ID, ToolCall: &ToolEvent{
						ID: tc.ID, Name: tc.Name, Input: tc.Input, StartedAt: startedAt,
					}})
					emit(out, RunEvent{Kind: EventToolResult, SessionID: s.ID, ToolResult: &ToolEvent{
						ID: tc.ID, Name: tc.Name, Output: capMsg, IsError: true,
						StartedAt: startedAt, EndedAt: time.Now().UTC(),
					}})
					s.Append(llm.Message{Role: llm.RoleTool, Content: capMsg, ToolCallID: tc.ID, ToolName: tc.Name})
					continue
				}
			}
			if rec := l.turnRecorder(); rec != nil && turnID != "" {
				// Best-effort bump on the in-flight row so /logs reflects
				// the running count even while the turn is mid-stream.
				go func(id string) {
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = rec.IncrementToolCalls(cctx, id)
				}(turnID)
			}
			l.fireHookT(turnID, "PreToolUse", s.ID, s.Project, tc.Name, map[string]any{
				"name":         tc.Name,
				"input":        tc.Input,
				"tool_call_id": tc.ID,
			})

			// Self-heal source guard: while the boss is in the conversation, a
			// heal pass may not rewrite Infinity's own code. The intended change
			// is filed as a code proposal; the model is told to answer the boss.
			if inSelfHealPass && steerCh != nil && isSourceMutation(tc) {
				refusal := l.refuseSelfHealSourceChange(ctx, s.ID, tc)
				endedAt := time.Now().UTC()
				emit(out, RunEvent{Kind: EventToolCall, SessionID: s.ID, ToolCall: &ToolEvent{
					ID: tc.ID, Name: tc.Name, Input: tc.Input, StartedAt: startedAt,
				}})
				emit(out, RunEvent{Kind: EventToolResult, SessionID: s.ID, ToolResult: &ToolEvent{
					ID: tc.ID, Name: tc.Name, Output: refusal, IsError: true,
					StartedAt: startedAt, EndedAt: endedAt,
				}})
				l.fireHookT(turnID, "ToolGated", s.ID, s.Project, tc.Name+": self-heal source guard", map[string]any{
					"name": tc.Name, "input": tc.Input, "reason": "self-heal source guard", "tool_call_id": tc.ID,
				})
				s.Append(llm.Message{Role: llm.RoleTool, Content: refusal, ToolCallID: tc.ID, ToolName: tc.Name})
				toolErredThisTurn = true
				continue
			}

			// Consent gate: while the boss is talking it through (stance
			// discuss), tools that start work or create durable things are
			// held. He gets a proposal, not a build. See consent.go.
			if hold, why := consentBlocks(ctx, tc.Name); hold {
				refusal := discussRefusal(tc.Name, why)
				endedAt := time.Now().UTC()
				emit(out, RunEvent{Kind: EventToolCall, SessionID: s.ID, ToolCall: &ToolEvent{
					ID: tc.ID, Name: tc.Name, Input: tc.Input, StartedAt: startedAt,
				}})
				emit(out, RunEvent{Kind: EventToolResult, SessionID: s.ID, ToolResult: &ToolEvent{
					ID: tc.ID, Name: tc.Name, Output: refusal, IsError: true,
					StartedAt: startedAt, EndedAt: endedAt,
				}})
				l.fireHookT(turnID, "ToolGated", s.ID, s.Project, tc.Name+": talk first", map[string]any{
					"name": tc.Name, "input": tc.Input, "reason": "boss is discussing, not asking for work", "tool_call_id": tc.ID,
				})
				s.Append(llm.Message{Role: llm.RoleTool, Content: refusal, ToolCallID: tc.ID, ToolName: tc.Name})
				// Like a boss denial: nothing ran, and it is not a failure to
				// heal around. Neither counter moves.
				continue
			}

			decision := l.gate.Authorize(ctx, s.ID, s.Project, tc.Name, tc.Input)

			// Surface the tool call to Studio. When the gate parked it on
			// a contract, Studio renders inline Approve/Deny buttons so
			// the boss can decide right in the chat. Otherwise the card
			// shows the spinner / "running" state until the result lands.
			awaiting := !decision.Allow && decision.WaitForApproval && decision.ContractID != ""
			emit(out, RunEvent{
				Kind:      EventToolCall,
				SessionID: s.ID,
				ToolCall: &ToolEvent{
					ID:               tc.ID,
					Name:             tc.Name,
					Input:            tc.Input,
					StartedAt:        startedAt,
					AwaitingApproval: awaiting,
					ContractID:       decision.ContractID,
					Preview:          decision.Preview,
				},
			})
			// And write down the CALL, not only the result - for EVERY brain.
			//
			// The self-executing branch above has filed a `running` row since
			// the "refreshed mid-turn and got nothing" fix; this path did not,
			// so on DeepSeek, OpenAI and the API brains a reload during a
			// five-minute command showed the boss his own message with nothing
			// under it. Same row shape, same tool_call_id; the result below
			// overwrites it in place. The gate's verdict rides along so a
			// reload during a parked approval shows the Approve/Deny card
			// rather than a spinner.
			l.fireHookT(turnID, "PostToolUse", s.ID, s.Project,
				tc.Name+" (running)", map[string]any{
					"name":              tc.Name,
					"input":             tc.Input,
					"tool_call_id":      tc.ID,
					"running":           true,
					"awaiting_approval": awaiting,
					"contract_id":       decision.ContractID,
				})

			var (
				output  string
				execErr error
				endedAt time.Time
				// interruptSteers are boss messages that arrived while a
				// SteerInterruptible tool ran and were consumed to stop it; they
				// are appended as user turns right after this tool's result.
				interruptSteers []Steer
				// gateRefused: the gate blocked this call and it never ran, with
				// no Trust contract pending. A wrong-bridge redirect, or a hard
				// refusal. Distinct from a boss denial (he answered; respect it).
				gateRefused bool
				// bossDenied: the boss explicitly declined the Trust contract.
				// Not progress (nothing ran), but ALSO not an error to self-heal
				// around — the agent must never treat the boss's "no" as a
				// failure to be worked around.
				bossDenied bool
				// planStartFailed: every gate allowed the call, but the plan
				// step could not be marked in flight, so the call was NOT run.
				// Executing anyway would put real work ahead of the board.
				planStartFailed bool
			)

			switch {
			case decision.Allow:
				// Every gate has allowed this call, and it hasn't run yet: the
				// one moment where the plan can be brought level with the work
				// (plan_start.go). A failure here stops the call outright.
				if startErr := l.startPlanStepForTool(ctx, s.ID, tc.Name); startErr != nil {
					output, planStartFailed = planStartRefusal(tc.Name, startErr), true
					endedAt = time.Now().UTC()
					break
				}
				// Inject the session's ActiveSet so session-aware tools
				// (load_tools / unload_tools / compact_context) can
				// mutate the right session's loaded list.
				toolCtx := tools.WithSessionID(tools.WithActiveSet(ctx, s.Active), s.ID)
				output, execErr, interruptSteers = l.executeInterruptible(toolCtx, tc, steerCh)
				endedAt = time.Now().UTC()
				l.recordToolCost(tc.Name, endedAt.Sub(startedAt), execErr)

			case decision.WaitForApproval && decision.ContractID != "":
				// Block on the gate's wait-for-approval channel. The
				// inline buttons in Studio POST to /api/trust-contracts
				// to flip the row's status; WaitForDecision returns when
				// that lands. On approve we run the same tool call we
				// were going to run - output streams into the SAME card.
				timeout := decision.WaitTimeout
				if timeout <= 0 {
					timeout = 15 * time.Minute
				}
				l.fireHookT(turnID, "ToolGated", s.ID, s.Project, tc.Name+": "+decision.Reason, map[string]any{
					"name":        tc.Name,
					"input":       tc.Input,
					"reason":      decision.Reason,
					"contract_id": decision.ContractID,
				})
				approved, reason := l.gate.WaitForDecision(ctx, decision.ContractID, timeout)
				if approved {
					// Same boundary as the allow path: the boss has just said go,
					// so the step goes in flight before the work does.
					if startErr := l.startPlanStepForTool(ctx, s.ID, tc.Name); startErr != nil {
						output, planStartFailed = planStartRefusal(tc.Name, startErr), true
					} else {
						toolCtx := tools.WithSessionID(tools.WithActiveSet(ctx, s.Active), s.ID)
						output, execErr, interruptSteers = l.executeInterruptible(toolCtx, tc, steerCh)
						l.recordToolCost(tc.Name, time.Since(startedAt), execErr)
					}
				} else {
					if reason == "" {
						reason = "denied or expired"
					}
					output = "BLOCKED: " + tc.Name + " " + reason + "\n" +
						"The boss did not approve this call. Do NOT retry it on your own. Instead, in your reply: " +
						"say in ONE plain sentence what you were trying to do and why (no tool names, no JSON — " +
						"describe it like you'd say it out loud), and tell him that if he wants it after all, " +
						"saying so will bring the approval card back. His denial may mean 'no', or it may mean " +
						"'I couldn't tell what this was' — give him the words to tell you which."
					bossDenied = true
				}
				endedAt = time.Now().UTC()

			default:
				endedAt = time.Now().UTC()
				gateRefused = true
				output = formatGatedOutput(tc.Name, decision)
				l.fireHookT(turnID, "ToolGated", s.ID, s.Project, tc.Name+": "+decision.Reason, map[string]any{
					"name":        tc.Name,
					"input":       tc.Input,
					"reason":      decision.Reason,
					"contract_id": decision.ContractID,
				})
			}

			// Mac->Cloud failover: if a claude_code__* (Mac-only) tool errored
			// because the Mac bridge is down, don't surface a raw ERROR the
			// model will retry to the iteration cap. Invalidate the health cache
			// (so the next turn routes to Cloud + hides claude_code__*), drop a
			// one-line heads-up into the chat stream, and hand back a structured
			// directive pointing at the cloud primitives. Consuming execErr is
			// what stops the retry death-spiral.
			if execErr != nil {
				if fb, ok := l.maybeBridgeFailover(out, s, tc.Name); ok {
					output, execErr = fb, nil
				}
			}

			isErr := execErr != nil
			switch {
			case isErr:
				output = fmt.Sprintf("ERROR: %v", execErr)
				toolErredThisTurn = true // feeds the reactive self-heal check
			case planStartFailed:
				// Nothing ran, and the reason is our own plan store failing —
				// exactly the kind of failure that must read as one rather than
				// pass for a quiet no-op. output already carries the refusal.
				isErr = true
				toolErredThisTurn = true
			case gateRefused:
				// A refused call NEVER RAN. Counting it as forward progress was
				// how a turn could end on "the bridge rejected it" while the
				// loop believed the segment had succeeded — no self-heal, no
				// continuation, just a shrug handed to the boss. It is the
				// opposite of progress: something the agent must work around.
				toolErredThisTurn = true
			case bossDenied:
				// The boss said no. Nothing ran, so it isn't progress — but it
				// isn't a failure either: self-heal must never be pointed at
				// working around the boss's explicit decision. Neither counter
				// moves.
			default:
				// Forward progress for the continuation gate: a segment with
				// zero successful tool results is thrash and won't be continued.
				segmentSuccesses++
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
			l.fireHookT(turnID, hookName, s.ID, s.Project, tc.Name+": "+output, map[string]any{
				"name":         tc.Name,
				"input":        tc.Input,
				"output":       output,
				"tool_call_id": tc.ID,
			})

			// Trim and mark ONLY the transcript copy: the full output already
			// went to the UI (EventToolResult) and to memory (PostToolUse hook)
			// above. Trimming keeps a fat result from being re-read in full on
			// every subsequent LLM call this turn; wrapping marks results that
			// are a third party's words so they can never read as the boss's.
			// Order is load bearing — see wrapUntrusted.
			s.Append(llm.Message{
				Role:       llm.RoleTool,
				Content:    wrapUntrusted(tc.Name, trimToolResult(tc.Name, output)),
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
			})

			// The boss's mid-run messages that stopped this tool land right
			// after its result, so the next LLM call reads them as the freshest
			// instruction (the ws layer already persisted them via the hook).
			if len(interruptSteers) > 0 {
				for _, st := range interruptSteers {
					text := strings.TrimSpace(st.Text)
					if text == "" && len(st.Attachments) == 0 {
						continue
					}
					s.Append(llm.Message{Role: llm.RoleUser, Content: text, Attachments: st.Attachments})
				}
				emit(out, RunEvent{Kind: EventSteered, SessionID: s.ID})
			}
		}
	}

	// Reached only after every segment was spent (or a segment made zero
	// progress). Keep the "iteration cap" phrasing so errs.Humanize still
	// categorises it as a safety-limit stop; add the round/call counts so the
	// run narrative reads honestly about how far it got.
	err := fmt.Errorf("hit the tool-iteration cap after %d tool calls across %d round(s) without finishing", toolCallCount, segment+1)
	emitHumanError(out, s.ID, err.Error())
	l.closeTurn(context.Background(), turnID, TurnCloseFields{
		StopReason:    "iteration_cap",
		Status:        "errored",
		ToolCallCount: toolCallCount,
		Error:         err.Error(),
		Summary:       summarizeReply("", toolCallCount),
	})
	// Settle the plan + title the session even on a segment-capped turn — a
	// long, tool-heavy turn that hits the cap did real work and should not be
	// left with a spinning step or a nameless session. No assistant text is in
	// scope here (partialText is per-iteration); the namer drafts from turnText.
	l.settlePlanOnTurnEnd(s.ID)
	if l.namer != nil {
		l.namer.MaybeName(s.ID, turnText, "")
	}
	return err
}

// drainSteer pulls every queued steer message off the channel non-blockingly
// and appends each as a User turn on the in-memory session. Called between
// iterations so the next LLM call sees the steer input alongside the original
// prompt and any intermediate tool results. Empty/whitespace strings are
// dropped.
//
// The UserPromptSubmit hook (mem_observations persistence) is now fired by
// the WS handler in steerTurn immediately on receipt, before the message
// lands in this channel. That way the observation is committed to Postgres
// without waiting for the next iteration boundary, so a navigation/reload
// that arrives while the turn is still in flight will find the steer in
// fetchSessionMessages instead of losing it.
func (l *Loop) drainSteer(ch <-chan Steer, s *Session) int {
	if ch == nil || s == nil {
		return 0
	}
	drained := 0
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return drained
			}
			text := strings.TrimSpace(msg.Text)
			if text == "" && len(msg.Attachments) == 0 {
				continue
			}
			s.Append(llm.Message{Role: llm.RoleUser, Content: text, Attachments: msg.Attachments})
			drained++
		default:
			return drained
		}
	}
}

// IsSyntheticSessionID reports whether a session id is a synthetic sub-agent
// bucket rather than a real UUID-keyed conversation. Three flavours exist, all
// minted with a scheme prefix: ephemeral delegates ("delegate:<uuid>"),
// persistent named peers ("peer:<slug>"), and detached background builds
// ("background:<uuid>"). NONE of these are valid UUIDs, so every mem_* /
// honcho write keyed on session id would throw SQLSTATE 22P02 ("invalid input
// syntax for type uuid"). Persistence is skipped for all of them: a delegate's
// only output is the summary it hands back (captured by the PARENT session),
// a peer's accumulated context lives in its in-memory session, and a
// background run reports progress through the mem_runs substrate (keyed by
// run id, not session id) — never through the UUID-typed capture path. This
// guard is what stops the "invalid input syntax for type uuid" storm and keeps
// sub-agent chatter out of memory and Honcho.
func IsSyntheticSessionID(id string) bool {
	return strings.HasPrefix(id, delegateSessionIDPrefix) ||
		strings.HasPrefix(id, peerSessionPrefix) ||
		strings.HasPrefix(id, backgroundSessionIDPrefix)
}

func (l *Loop) fireHook(name, sessionID, project, text string, payload map[string]any) {
	if l.hooks == nil || IsSyntheticSessionID(sessionID) {
		return
	}
	l.hooks.Emit(name, sessionID, project, text, payload)
}

// fireHookT is fireHook but with a turn_id auto-injected into the payload
// so capture.go / predict.go can stamp it on the mem_observations /
// mem_predictions row. When turnID is empty (no recorder wired) the
// payload is left untouched.
func (l *Loop) fireHookT(turnID, name, sessionID, project, text string, payload map[string]any) {
	if l.hooks == nil || IsSyntheticSessionID(sessionID) {
		return
	}
	if turnID != "" {
		if payload == nil {
			payload = map[string]any{}
		}
		// Don't clobber an explicit turn_id the caller already set.
		if _, ok := payload["turn_id"]; !ok {
			payload["turn_id"] = turnID
		}
	}
	l.hooks.Emit(name, sessionID, project, text, payload)
}

// closeTurn stamps the final outcome on the mem_turns row. Safe to call with
// empty turnID (no-op when the recorder isn't wired or the open failed).
// Runs synchronously because the row needs to flip status before the next
// turn might tail it - the operation is a single indexed UPDATE so it's
// sub-ms in practice.
func (l *Loop) closeTurn(ctx context.Context, turnID string, fields TurnCloseFields) {
	rec := l.turnRecorder()
	if rec == nil || turnID == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rec.Close(cctx, turnID, fields); err != nil {
		log.Printf("turn close: id=%s err=%v", turnID, err)
	}
}

// summarizeReply builds the short summary the /logs list view renders. For
// successful turns it's the first ~140 chars of the assistant reply; for
// empty replies (model returned no text - usually a tool-only iteration
// cap or a confused decode) it's a synthetic marker so the row is still
// readable. Trims whitespace and collapses newlines.
func summarizeReply(text string, toolCalls int) string {
	t := strings.TrimSpace(text)
	if t == "" {
		if toolCalls > 0 {
			return fmt.Sprintf("(no reply - %d tool call%s)", toolCalls, plural(toolCalls))
		}
		return "(no reply)"
	}
	t = strings.ReplaceAll(t, "\n", " ")
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > 140 {
		t = t[:140] + "…"
	}
	return t
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// emitWait caps how long a producer waits on a full channel before it gives
// up on ONE event. Long enough to ride out any real consumer hiccup, short
// enough that a genuinely dead reader cannot strand a turn forever.
const emitWait = 5 * time.Second

// emit hands one event to the consumer. It DROPS only as a last resort, and
// says so out loud when it does.
//
// This used to be a bare `select { case ch <- ev: default: }` - throw the
// event away the instant the buffer was full - and that is the bug behind
// "why must I always refresh to see what the agent does" (2026-09-01).
//
// The reason it only ever bit the Claude Max brain: every other provider
// streams at the pace the model writes, a few events at a time, so a 128-slot
// buffer is never close to full. The Claude brain reads its transcript on a
// 300ms poll and emits a whole batch at once - one real turn of the boss's
// carried 44 tool-argument deltas, 18 text deltas and 14 block boundaries in
// single bursts. The buffer filled, the remainder went in the bin, and the
// EventComplete at the end of the turn went with it. So the answer existed,
// was written to the database, and the browser sat spinning on a turn that
// had been over for twenty minutes: it never got told.
//
// A dropped delta is a cosmetic loss. A dropped completion is a lie. Neither
// is worth the microsecond the old version saved, and both consumers on the
// live path (the loop's stream reader, ws.runTurn) drain until close, so
// waiting cannot deadlock them.
func emit(ch chan<- RunEvent, ev RunEvent) {
	if ch == nil {
		return
	}
	// Fast path: room in the buffer, no timer, no allocation.
	select {
	case ch <- ev:
		return
	default:
	}
	t := time.NewTimer(emitWait)
	defer t.Stop()
	select {
	case ch <- ev:
	case <-t.C:
		// Never silent. A consumer this far behind is a real fault, and the
		// boss is about to see a turn that looks stuck.
		log.Printf("agent: consumer stalled %s, dropped a %s event for session %s", emitWait, ev.Kind, ev.SessionID)
	}
}

// maybeBridgeFailover handles a claude_code__* (Mac-only) tool error by failing
// over to the cloud bridge when the Mac bridge is unhealthy. It invalidates the
// router health cache (so the next turn routes to Cloud and the visibility filter
// hides the claude_code__* schemas), emits a deterministic one-line heads-up into
// the chat stream, and returns a structured directive the model can act on this
// turn. Returns (directive, true) when it handled the error; ("", false) to let
// the normal ERROR path run (non-claude_code tool, no router, or Mac healthy so
// the failure is a real tool error, not a bridge outage).
func (l *Loop) maybeBridgeFailover(out chan<- RunEvent, s *Session, toolName string) (string, bool) {
	if !strings.HasPrefix(toolName, "claude_code__") {
		return "", false
	}
	l.providerMu.RLock()
	router := l.bridgeRouter
	l.providerMu.RUnlock()
	if router == nil || router.MacBridgeHealthy() {
		return "", false
	}
	router.Invalidate()
	cloudHealthy := router.Snapshot().CloudHealthy

	notice := "\n\n_Mac bridge went offline — continuing on the cloud workspace._\n"
	if !cloudHealthy {
		notice = "\n\n_Mac bridge went offline and the cloud workspace is also unreachable — I can't make code changes until a bridge is back._\n"
	}
	emit(out, RunEvent{Kind: EventDelta, SessionID: s.ID, TextDelta: notice, MsgIndex: s.replyIndex})
	return macBridgeDownFallback(toolName, cloudHealthy), true
}

// macBridgeDownFallback is the structured tool result handed to the model when a
// claude_code__* call dies on a downed Mac bridge — a deterministic directive
// (Rule #1b: the mechanic is in code, this only tells the model where to go), not
// a raw ERROR it would retry to the iteration cap.
func macBridgeDownFallback(toolName string, cloudHealthy bool) string {
	payload := map[string]any{
		"error":     "mac_bridge_unavailable",
		"tool":      toolName,
		"both_down": !cloudHealthy,
	}
	if cloudHealthy {
		payload["fallback"] = "The Mac bridge is offline. Do NOT retry this claude_code__* call. load_tools the cloud primitives (fs_read, fs_ls, fs_save, fs_edit, bash_run, git_status, git_diff, git_stage, git_commit, git_push, git_pull) and redo this change on /workspace."
		payload["important"] = "Cloud /workspace is a git-synced clone, NOT the same filesystem as the Mac — run `git -C /workspace/<repo> pull` first so you edit current code, then commit + push when done."
	} else {
		payload["fallback"] = "Both the Mac bridge and the cloud workspace are unreachable. Do NOT retry. Tell the boss code changes can't be made until a bridge is back online."
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// formatGatedOutput is the synthetic tool result shown to the LLM when a
// gate blocks execution. It tells the model what actually happened -
// success-or-failure honesty matters here because the model will paraphrase
// the result to the user, and if we lie ("queued") when the row was never
// persisted the user gets a phantom Trust contract that never appears.
//
// A Redirect decision is a different animal from an approval gate: the call was
// simply WRONG and Reason says how to do it right. It must never mention the
// boss, approval, or the Trust store - the model's job is to retry, in this
// same turn, with the tool it was just pointed at.
func formatGatedOutput(toolName string, d GateDecision) string {
	var b strings.Builder
	if d.Redirect {
		b.WriteString("NOT RUN: the call to ")
		b.WriteString(toolName)
		b.WriteString(" was wrong, and was corrected before it executed.\n")
		if d.Reason != "" {
			b.WriteString(d.Reason)
			b.WriteString("\n")
		}
		// Deliberately never names the concepts it is trying to rule out
		// ("approval", "Trust store", "queued"): a negated concept is still a
		// concept, and this model reliably drops the negation and keeps the
		// noun. Say only what to do.
		b.WriteString("This is a correction to you, not a fault in the system. Nothing is pending and nothing ")
		b.WriteString("needs the boss. Say nothing about it to him, report no failure, and retry NOW with the ")
		b.WriteString("tool named above.")
		return b.String()
	}
	// A plain refusal (Allow=false, no approval wanted, no contract): the
	// loop guard stopping a call that fired four times with identical inputs,
	// a gate saying "not this, ever". It was never queued and never will be,
	// so it must not read as an approval request. The one time it did, the
	// model told the boss to approve it in the Trust tab and he found the tab
	// empty (2026-09-04, DeepSeek marking rows on a table that does not
	// exist, four times in a minute). Same lesson as Redirect: never name the
	// concept being ruled out.
	if !d.WaitForApproval && d.ContractID == "" {
		b.WriteString("NOT RUN: the call to ")
		b.WriteString(toolName)
		b.WriteString(" was refused.\n")
		if d.Reason != "" {
			b.WriteString(d.Reason)
			b.WriteString("\n")
		}
		b.WriteString("Nothing is pending and nothing needs the boss. Do not repeat this call as it was. ")
		b.WriteString("Change the input or the approach, or ask him what to do, and if you mention it at all, ")
		b.WriteString("say only that the call was refused and why.")
		return b.String()
	}
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
		b.WriteString("DO NOT tell the boss it was queued - the gate fired but the row failed to land. ")
		b.WriteString("Tell the boss the Trust store is misconfigured and the action was simply refused.")
	}
	return b.String()
}

// emitHumanError is the single seam every provider/runtime failure passes
// through on its way to the boss's screen. Raw text goes to the logs; the chat
// gets Jarvis's own words.
//
// This exists because the boss was shown a bare provider SSE payload mid-chat
// ({"type":"error","error":{"type":"server_error",...},"sequence_number":22}).
// That is the failure-copy rule broken at the last inch: every other surface
// (mem_runs.human_error, cron status, pings) already routes through
// errs.Humanize, and the live chat, the surface he actually watches, did not.
// Humanizing HERE rather than at each call site means a new error path can't
// quietly reintroduce raw JSON.
func emitHumanError(out chan<- RunEvent, sessionID, raw string) {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		emit(out, RunEvent{Kind: EventError, SessionID: sessionID, Error: "Something went wrong and I stopped."})
		return
	}
	log.Printf("turn error: session=%s err=%s", sessionID, msg)
	h := errs.HumanizeString(msg)
	parts := []string{}
	for _, p := range []string{h.Title, h.Summary, h.Action} {
		if p = strings.TrimSpace(p); p != "" {
			if !strings.HasSuffix(p, ".") && !strings.HasSuffix(p, "!") && !strings.HasSuffix(p, "?") {
				p += "."
			}
			parts = append(parts, p)
		}
	}
	human := strings.Join(parts, " ")
	// An unclassified failure keeps its raw text visible: a prettier sentence
	// that hides what actually broke would be worse than the JSON it replaced.
	if h.Category == errs.CatUnknown {
		human = h.Title + ". " + firstLineOf(msg)
	}
	emit(out, RunEvent{Kind: EventError, SessionID: sessionID, Error: human})
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

// executeInterruptible runs a tool call, and for tools that opted into
// tools.SteerInterruptible in an INTERACTIVE session (steerCh non-nil), stops
// WAITING on it the moment the boss sends a new message. What "stops" means
// depends on two things, and on nothing tool-specific:
//
//   - Is the message an explicit stop? isStopIntent decides, deterministically
//     (no LLM call: a running build must not depend on a classifier).
//   - Does the tool's work outlive the turn? tools.SteerDetachable declares it.
//
// A stop order, or a tool whose work dies with the turn → the tool ctx is
// cancelled and the tool tears its work down (today's behaviour). Anything
// else on a detachable tool → the DETACH signal fires: the tool returns what
// it can say now, its job keeps running, and it reports back on its own.
// Either way the consumed steer(s) come back so the caller appends them right
// after the tool result and the boss is answered immediately. Short tools and
// autonomous runs are untouched.
func (l *Loop) executeInterruptible(ctx context.Context, tc llm.ToolCall, steerCh <-chan Steer) (string, error, []Steer) {
	if steerCh == nil || l.tools == nil || !l.tools.InterruptsOnSteer(tc.Name) {
		out, err := l.tools.Execute(ctx, tc)
		return out, err, nil
	}
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Detachable tools get the "keep working" wire. Handing it out costs
	// nothing for tools that never read it.
	detachable := l.tools.DetachesOnSteer(tc.Name)
	tctx, detach := tools.WithDetachSignal(tctx)
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		o, e := l.tools.Execute(tctx, tc)
		done <- result{o, e}
	}()
	select {
	case r := <-done:
		return r.out, r.err, nil
	case st, ok := <-steerCh:
		if !ok {
			r := <-done
			return r.out, r.err, nil
		}
		// Read every message that is already queued BEFORE deciding: "how's
		// it going" followed by "actually stop" is a stop.
		steers := []Steer{st}
	drain:
		for {
			select {
			case more, ok2 := <-steerCh:
				if !ok2 {
					break drain
				}
				steers = append(steers, more)
			default:
				break drain
			}
		}
		kill := !detachable || anyStopIntent(steers)
		if kill {
			cancel()
		} else {
			detach()
		}
		r := <-done
		var b strings.Builder
		if kill {
			fmt.Fprintf(&b, "INTERRUPTED: the boss sent a new message while %s was running, so it was stopped to answer him first. "+
				"His message follows as the next user turn — respond to THAT before anything else, and do not restart this call unless he asks.\n\n", tc.Name)
		} else {
			// The truth, because the old sentinel's "do not restart this call
			// unless he asks" is what left long jobs dead: the model believed
			// the work had ended. It has NOT.
			fmt.Fprintf(&b, "STILL RUNNING: the boss sent a new message while %s was working, so it was DETACHED, not stopped — "+
				"the job is still going and its result below names the run it is on and how it reports back when it lands. "+
				"Do NOT start it again (that would run the same work twice) and do NOT tell him it was cancelled. "+
				"His message follows as the next user turn — answer THAT now, and say in one line that the work is still running.\n\n", tc.Name)
		}
		b.WriteString(strings.TrimSpace(r.out))
		if r.err != nil {
			if kill {
				b.WriteString("\n(stop detail: " + r.err.Error() + ")")
			} else {
				b.WriteString("\n(detach detail: " + r.err.Error() + ")")
			}
		}
		return b.String(), nil, steers
	}
}
