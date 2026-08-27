package llm

import (
	"context"
	"encoding/json"
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

var ErrNotImplemented = errors.New("provider not implemented")
