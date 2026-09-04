package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// Attachments are the files attached to a user message (images, PDFs,
	// text). Providers ship them as native blocks ahead of Content; see
	// attachment.go. Bytes are excluded from JSON by design.
	Attachments []Attachment   `json:"attachments,omitempty"`
	ToolCalls   []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	ToolName    string         `json:"tool_name,omitempty"`
	Timestamp   time.Time      `json:"timestamp,omitempty"`
	Meta        map[string]any `json:"-"`
	// Volatile is the per-turn context (retrieved memory, current time, the
	// plan, the discuss overlay) PINNED to the user message that opened the
	// turn. Providers render it as a trailing block on that message and
	// nowhere else, so it sits at the same byte offset on every call of the
	// turn and the cached prefix never moves.
	//
	// It used to ride at the tail of the request instead: after the last
	// message on Anthropic, as a trailing developer/system message on the
	// OpenAI family. Every tool round trip pushed it one message further
	// down, which changed the bytes of the message it had just left, so each
	// call re-wrote the whole turn's tool traffic at full price (quadratic in
	// tool calls). Claude Code keeps its reminders on the message they were
	// born on for exactly this reason. The loop clears it when the turn
	// ends, so a session does not accumulate a stale copy per turn.
	Volatile string `json:"-"`
}

// VolatileBlock renders the pinned per-turn context as the trailing text of
// its message, framed so the model reads it as reference material rather
// than as a second request. "" when there is nothing pinned.
func (m Message) VolatileBlock() string {
	v := strings.TrimSpace(m.Volatile)
	if v == "" {
		return ""
	}
	return volatileMessageCaption + "\n\n" + v
}

// volatileMessageCaption frames the pinned block. The message it rides on
// IS the request, so the caption points back up rather than at "the
// conversation above".
const volatileMessageCaption = "Background context assembled for this message (retrieved memory, current time, the plan). Reference material for answering the message above, not a new request."

// SelfExecutingProvider is a brain that runs its OWN tools and hands back a
// finished turn, rather than returning tool calls for our loop to execute.
// Claude Code is one: it has its own agent loop, its own tools, its own
// session. Response.ToolCalls comes back empty from these, so the loop knows
// that anything they streamed already happened and belongs in his ledger and
// in memory as it arrives, in the order it arrives.
//
// Declared as a capability rather than sniffed from the provider's name, and
// answered at stream time rather than inferred afterwards: doing it after the
// stream closed put the tool rows AFTER the reply, and Studio, seeing a tool
// row last, told him no reply had come when the answer was sitting right
// above it.
type SelfExecutingProvider interface {
	RunsOwnTools() bool
}

const ResponseItemMetaKey = "openai_response_item"

func WithRawResponseItem(m Message, raw json.RawMessage) Message {
	if len(raw) == 0 {
		return m
	}
	if m.Meta == nil {
		m.Meta = map[string]any{}
	}
	cp := append(json.RawMessage(nil), raw...)
	m.Meta[ResponseItemMetaKey] = cp
	return m
}

func RawResponseItem(m Message) (json.RawMessage, bool) {
	if m.Meta == nil {
		return nil, false
	}
	switch v := m.Meta[ResponseItemMetaKey].(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return nil, false
		}
		return v, true
	case []byte:
		if len(v) == 0 {
			return nil, false
		}
		return json.RawMessage(v), true
	case string:
		if v == "" {
			return nil, false
		}
		return json.RawMessage(v), true
	default:
		return nil, false
	}
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
	// Input is the count of *uncached* prompt tokens billed at full rate.
	// Each provider normalizes its raw usage into this field so the cost
	// ledger can apply one cache-discount formula regardless of vendor
	// (Anthropic already reports input_tokens net of cache; OpenAI's
	// prompt_tokens includes cached, so its serializer subtracts CacheRead).
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
	// CacheRead / CacheWrite are the prompt-caching breakdown. CacheRead is
	// billed at ~0.1x (Anthropic) / heavily discounted (OpenAI); CacheWrite
	// at ~1.25x (Anthropic only; OpenAI auto-caching has no separate write
	// charge). Zero when the provider doesn't cache or the prompt missed.
	// These tokens STILL occupy the context window and STILL count toward
	// rate limits - only their dollar cost is discounted.
	CacheRead  int `json:"cache_read_input_tokens,omitempty"`
	CacheWrite int `json:"cache_creation_input_tokens,omitempty"`
	// ContextTokens is how full the WINDOW got, when that is a different
	// number from the prompt tokens billed.
	//
	// For every brain that answers in one API call they are the same, and
	// this stays 0. A brain that runs its own tool loop (Claude Code) answers
	// one of our turns with MANY calls and reports the SUM: one real turn of
	// the boss's billed 2,172,488 cache-read tokens across 13 calls, while
	// the deepest single prompt - the actual window fill - was 172,498. The
	// meter divided the sum by a 1M window, showed him 217% and sat red for
	// an hour, and auto-compaction fired on a window that was a fifth full.
	//
	// So the two questions are kept apart: PromptTokens answers "what did
	// this cost", ContextTokens answers "how full is he".
	ContextTokens int `json:"context_tokens,omitempty"`
}

// WindowTokens is how much of the context window the last call occupied.
// Falls back to PromptTokens, which is the same number for every brain that
// answers in a single call.
func (u TokenUsage) WindowTokens() int {
	if u.ContextTokens > 0 {
		return u.ContextTokens
	}
	return u.PromptTokens()
}

// PromptTokens is the FULL prompt size that occupied the context window and
// counted toward rate limits: uncached input + cache reads + cache writes.
// ALWAYS use this for context-meter / window / rate-limit math - never bare
// Input, which providers normalize down to the uncached remainder so the cost
// ledger can apply the cache discount. (A cache HIT reports most of the prompt
// under CacheRead with a tiny Input; counting only Input would read near-empty
// on a full window.)
func (u TokenUsage) PromptTokens() int { return u.Input + u.CacheRead + u.CacheWrite }

// ThroughputTokens is the raw token count for the cost ledger's `quantity`
// column - the true number of tokens that moved through the model this call
// (full prompt + output), at face value with no discount. This keeps "how many
// tokens did I use" honest and consistent with pre-caching history.
func (u TokenUsage) ThroughputTokens() int { return u.PromptTokens() + u.Output }

// BilledTokens applies the prompt-cache price multipliers - cache reads bill at
// ~0.1x and writes at ~1.25x of the base input rate (Anthropic; OpenAI auto-
// cache reads are discounted with no write charge) - to produce the cost-
// weighted figure the USD estimate uses. Distinct from ThroughputTokens: this
// is what it COST, that is how many tokens MOVED.
func (u TokenUsage) BilledTokens() int {
	return u.Input + u.Output + u.CacheRead/10 + u.CacheWrite*5/4
}

type Response struct {
	Text       string     `json:"text"`
	ToolCalls  []ToolCall `json:"tool_calls"`
	Usage      TokenUsage `json:"usage"`
	StopReason string     `json:"stop_reason"`
	// Meta is provider-private carry for the assistant message the loop
	// appends from this response (Message.Meta): never persisted, never
	// rendered. DeepSeek uses it to get its own reasoning back on the next
	// request of a tool-calling round (MetaReasoningContent).
	Meta map[string]any `json:"-"`
}

// MetaReasoningContent is the Message.Meta / Response.Meta key carrying a
// reasoner's chain of thought for replay to the vendor that wants it back.
const MetaReasoningContent = "reasoning_content"

type StreamEvent struct {
	Kind          StreamEventKind `json:"kind"`
	TextDelta     string          `json:"text_delta,omitempty"`
	ThinkingDelta string          `json:"thinking_delta,omitempty"`
	// ThinkingTokens is a running count of reasoning tokens for THIS turn,
	// when the brain reports one instead of the reasoning itself.
	//
	// Claude Code is that brain: in -p mode it emits the thinking block and
	// its deltas with the text REDACTED (every thinking_delta arrives empty),
	// and reports progress as `system/thinking_tokens` counts instead. So the
	// boss got a row that said "Thinking" and a clock, with nothing under it,
	// for two minutes. This is the one real signal available, and it moves.
	ThinkingTokens int       `json:"thinking_tokens,omitempty"`
	ToolCall       *ToolCall `json:"tool_call,omitempty"`
	// Tool-input streaming: a StreamToolInputDelta carries the model writing a
	// tool call's arguments live, BEFORE the tool runs and before the final
	// StreamToolCall. ToolCallID/ToolName identify which call (set as soon as
	// the provider knows them); InputDelta is the raw partial-JSON argument
	// chunk. Model-agnostic: providers that can stream tool args emit these;
	// ones that can't simply fall back to the complete StreamToolCall, so
	// consumers treat deltas as a best-effort live preview that the final tool
	// call reconciles authoritatively.
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	InputDelta string `json:"input_delta,omitempty"`
	// ToolOutput / ToolError carry a StreamToolResult: what the tool returned
	// and whether it failed. ToolCallID says which call it answers.
	ToolOutput string      `json:"tool_output,omitempty"`
	ToolError  bool        `json:"tool_error,omitempty"`
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *TokenUsage `json:"usage,omitempty"`
	Err        string      `json:"err,omitempty"`
}

type StreamEventKind string

const (
	StreamText     StreamEventKind = "text"
	StreamThinking StreamEventKind = "thinking"
	StreamToolCall StreamEventKind = "tool_call"
	// StreamToolResult is what a tool a SELF-EXECUTING brain ran came back
	// with. Every other provider hands us the call and our loop produces the
	// result itself; a harness runs both halves inside its own session, so
	// without this the boss sees what it decided to do and never what
	// happened, and memory records the same half-story.
	StreamToolResult     StreamEventKind = "tool_result"
	StreamToolInputDelta StreamEventKind = "tool_input_delta"
	StreamComplete       StreamEventKind = "complete"
	StreamError          StreamEventKind = "error"
	// StreamNotice is a one-line heads-up for the boss that the provider layer
	// itself wants in the chat (TextDelta carries it): "ChatGPT is out of
	// usage until 10pm, I'm on Claude meanwhile". Rendered as ordinary reply
	// text by the agent loop; one-shot consumers (summarizer, classifier)
	// ignore it like any kind they don't handle.
	StreamNotice StreamEventKind = "notice"
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

// SystemPrompt carries the stable/volatile split so caching providers can put
// a cache breakpoint after the byte-identical stable segment. This is the
// generic prompt-caching contract: the loop declares the boundary ONCE, every
// provider honors it its own way (Anthropic: cache_control breakpoints;
// OpenAI/DeepSeek: automatic caching on the now-stable prefix). Stable must be
// byte-identical across a session's turns (the soul/base system prompt);
// Volatile holds per-turn content (RRF retrieval, current_time, tool catalog,
// account overlay, voice/wind-down).
type SystemPrompt struct {
	Stable   string
	Volatile string
}

// Render concatenates the two segments in stable-first order. Used by the
// non-caching Stream fallback so a provider that doesn't implement caching
// still benefits from the stable-first ordering (auto-cachers hit; others are
// unaffected).
func (s SystemPrompt) Render() string {
	switch {
	case s.Stable == "":
		return s.Volatile
	case s.Volatile == "":
		return s.Stable
	default:
		return s.Stable + "\n\n" + s.Volatile
	}
}

// volatileTailCaption frames the volatile segment when it rides at the TAIL of
// the message array instead of the system slot. Without it the model can read a
// trailing block of retrieved memory and tool listings as a fresh instruction
// and answer THAT instead of the conversation.
const volatileTailCaption = "Background context assembled for this turn (retrieved memory, available tools, connected accounts, current time). Reference material, not a new request: keep answering whatever the conversation above is asking."

// VolatileTail returns the volatile segment framed for delivery as the LAST
// item in the message array, or "" when there is nothing volatile to say.
//
// This is the OpenAI-family half of the caching contract. Anthropic can mark a
// breakpoint mid-system, so it keeps volatile in the system slot after the
// cached stable block (see anthropic.go). OpenAI cannot: per its prompt-caching
// guide the cacheable prefix is "the model's full rendered context including
// OpenAI-provided instructions, developer messages, tool definitions, and
// conversation history", and the `instructions` field "cannot contain explicit
// breakpoints". So anything volatile placed there sits at byte zero of the
// prefix and invalidates the system prompt, every tool schema AND the entire
// history behind it on every single turn. The guide's prescribed order is
// stable instructions first, dynamic content after, which is what moving the
// volatile segment to the tail achieves.
//
// Placement is a single decision shared by every OpenAI-family provider, so it
// lives here rather than in each one.
func (s SystemPrompt) VolatileTail() string {
	v := strings.TrimSpace(s.Volatile)
	if v == "" {
		return ""
	}
	return volatileTailCaption + "\n\n" + v
}

// CachingProvider is the OPTIONAL capability a Provider implements when it can
// exploit the stable/volatile split for prompt caching. The agent loop calls
// StreamCached when the provider (seen through any wrapper) implements it, and
// falls back to Stream with the rendered string otherwise - so non-caching
// providers and the ~10 one-shot Stream callers are completely unaffected.
type CachingProvider interface {
	StreamCached(ctx context.Context, model string, sys SystemPrompt, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error)
}

// CompactingProvider is the optional stateless context-compaction capability.
// Providers that expose an official compact endpoint return the canonical next
// message window. The caller must replace the old session messages with the
// returned slice exactly; individual messages may carry provider-native raw
// response items in Meta so a later Stream can pass them back without lossy
// re-encoding.
type CompactingProvider interface {
	CompactContext(ctx context.Context, model string, messages []Message) ([]Message, TokenUsage, error)
}

// MetaWorldState is the Message.Meta key marking a synthetic user-role
// message that carries world state (tool catalog, accounts, bridge) rather
// than something the boss said. Providers that flatten the transcript to
// text render it as context, not as his words.
const MetaWorldState = "world_state"

// IsWorldState reports whether a message is a world-state carrier.
func IsWorldState(m Message) bool {
	v, _ := m.Meta[MetaWorldState].(bool)
	return v
}

type noToolCallsCtxType struct{}

// WithNoToolCalls asks the provider to send the SAME tool definitions but
// forbid calling them (tool_choice: none) for this call. The compaction
// summarizer uses it: it must run against the conversation's exact prefix
// (tools included) to read from the warm cache, yet must answer with the
// summary rather than another tool call. Both vendors document this as the
// way to disable tools without invalidating the cached prefix.
func WithNoToolCalls(ctx context.Context) context.Context {
	return context.WithValue(ctx, noToolCallsCtxType{}, true)
}

// NoToolCallsFromContext reports whether WithNoToolCalls was set.
func NoToolCallsFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(noToolCallsCtxType{}).(bool)
	return v
}

type maxTurnsCtxType struct{}

// WithMaxTurns caps a self-executing brain's own tool loop for this call
// (Claude Code's --max-turns). Providers that run our loop ignore it: the
// agent loop already bounds them.
func WithMaxTurns(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, maxTurnsCtxType{}, n)
}

// MaxTurnsFromContext returns the cap set by WithMaxTurns, or 0.
func MaxTurnsFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	n, _ := ctx.Value(maxTurnsCtxType{}).(int)
	return n
}

// SessionForgetter is the optional capability of a brain that holds the
// conversation ITSELF (Claude Code): after the loop compacts its copy of
// the history, the brain's own session must be dropped too, or the next
// resumed turn continues on the uncompacted transcript. Reached through
// decorators via ForgetSessionIfSupported.
type SessionForgetter interface {
	ForgetSession(ctx context.Context, sessionID string)
}

// ForgetSessionIfSupported drops a self-held session on the innermost
// provider that can, and reports whether one did.
func ForgetSessionIfSupported(ctx context.Context, p Provider, sessionID string) bool {
	for p != nil {
		if f, ok := p.(SessionForgetter); ok {
			f.ForgetSession(ctx, sessionID)
			return true
		}
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return false
		}
		p = u.Unwrap()
	}
	return false
}

type cacheKeyCtxType struct{}

// WithCacheKey stamps a stable routing key (the session id) onto the context.
// OpenAI/OAuth providers forward it as `prompt_cache_key` so all turns of a
// session route to the same cache shard, raising the auto-cache hit rate.
// Anthropic ignores it (its caching is content-addressed via cache_control).
func WithCacheKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, cacheKeyCtxType{}, key)
}

// CacheKeyFromContext returns the routing key set by WithCacheKey, or "".
func CacheKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(cacheKeyCtxType{}).(string); ok {
		return v
	}
	return ""
}

// Effort is a per-turn reasoning-effort hint. It is the ONLY thing steal C
// varies; the model id it rides alongside is never changed. The five levels map
// to GPT's reasoning.effort enum (none|low|medium|high|xhigh) and to Anthropic's
// thinking budget. The zero value "" means "unset" - providers omit the field
// entirely and the model keeps its own default, so an un-stamped call (every
// non-loop caller) behaves exactly as it did before this existed.
type Effort string

const (
	EffortNone   Effort = "none"
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
)

// Valid reports whether e is one of the five known levels. "" is NOT valid here
// (it is the deliberate "omit" sentinel, handled by WithEffort as a no-op).
func (e Effort) Valid() bool {
	switch e {
	case EffortNone, EffortLow, EffortMedium, EffortHigh, EffortXHigh:
		return true
	}
	return false
}

type effortCtxType struct{}

// WithEffort stamps a per-call reasoning-effort hint onto the context. It is an
// exact mirror of WithCacheKey: a context value, NOT a Stream signature change,
// so every non-loop Stream/StreamCached caller and the noDashesProvider wrapper
// pass it through verbatim and simply never set it - preserving the boss default
// on aux calls (gauge/critic/summarizer/namer run under their own ctx). An empty
// or invalid level is a no-op (omit -> model default), which is what guarantees
// "never silently change reasoning depth/cost" out of the box.
func WithEffort(ctx context.Context, level Effort) context.Context {
	if !level.Valid() {
		return ctx
	}
	return context.WithValue(ctx, effortCtxType{}, level)
}

// EffortFromContext returns the level set by WithEffort, or "" when unset. A
// provider reads this INSIDE its existing reasoning gate and omits the field
// when it is "".
func EffortFromContext(ctx context.Context) Effort {
	if v, ok := ctx.Value(effortCtxType{}).(Effort); ok {
		return v
	}
	return ""
}

// SelfExecuting reports whether the brain behind p runs its own tools,
// unwrapping the sanitizer/failover decorators to ask the real provider.
//
// Every registered provider is wrapped (factory.go: WrapNoDashes, and
// failoverProvider on top of that), and neither wrapper implements
// SelfExecutingProvider, so a type assertion on what the loop is handed was
// ALWAYS false. The consequence was total and invisible: for every Claude Max
// turn the boss ever had, the loop believed the brain did not run tools, the
// branch that records its tool calls and results never executed, and nothing
// he did with that brain reached his ledger, his transcript, or memory. He
// watched it write files to his tree and saw nothing in the chat, and every
// reload showed him a spinner over an empty turn. Found 2026-09-02 after a
// night of fixes built inside that dead branch, each verified by a test that
// used a bare brain and so never met the wrapper.
//
// Same unwrap loop as Implemented, for the same reason: a capability must be
// asked of the brain, never of its coat.
func SelfExecuting(p Provider) bool {
	for p != nil {
		if se, ok := p.(SelfExecutingProvider); ok {
			return se.RunsOwnTools()
		}
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return false
		}
		p = u.Unwrap()
	}
	return false
}

var ErrNotImplemented = errors.New("provider not implemented")

// implementedReporter is satisfied by a provider that knows it is a stub.
// Absence means implemented - only a stub has to say so.
type implementedReporter interface{ Implemented() bool }

// Implemented reports whether a provider can actually answer a turn. Used at
// the two places where a stub would otherwise masquerade as a working brain:
// registry construction (do not offer it) and the provider-keys API (do not
// take a credential for it). Unwraps through the sanitizer/failover wrappers
// so a decorated stub is still recognised.
func Implemented(p Provider) bool {
	for p != nil {
		if r, ok := p.(implementedReporter); ok {
			return r.Implemented()
		}
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return true
		}
		p = u.Unwrap()
	}
	return true
}
