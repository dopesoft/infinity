package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// The Claude Max Plan brain.
//
// Every other provider in this package speaks to a model API: we send the
// whole conversation and a tool list, the model answers with text or tool
// calls, our agent loop executes them. This one is different by nature, and
// the difference is the point.
//
// Claude Code is an AGENT HARNESS, not a model endpoint. It owns its own loop,
// its own tools and its own context. So this provider does not ask it to
// answer one message - it hands it the turn and lets it work: read files, run
// commands, search the web, call Infinity's own tools over MCP, and come back
// when it has an answer. What our loop receives is the finished reply, with
// every step it took streamed through on the way so the boss watches it work
// exactly as he watches any other turn.
//
// Two things follow from that, and both are deliberate:
//
//   - ToolCalls on the Response are always empty. Claude Code executed its
//     tools itself, inside its own loop. Our loop sees a completed turn. This
//     is not a missing feature; a harness that returned half-finished tool
//     calls to a second loop would be two agents fighting over one
//     conversation.
//   - Infinity's tools still work, through MCP. The runner points the session
//     at Core's own MCP endpoint (server/mcp_server.go), which publishes the
//     SAME registry the chat loop uses, so memory writes, surface items and
//     connector calls all run through the usual gates and hooks. Without that
//     this would be a brilliant brain with amnesia.
//
// Billing: this runs on the boss's Claude MAX SUBSCRIPTION via the Mac
// bridge's own sign-in, never the Anthropic API key. That rule is enforced in
// the runner (it proves organizationType claude_max before launching and
// unsets ANTHROPIC_API_KEY for the run), not here, and it is not negotiable -
// see the coding-brain contract in CLAUDE.md.
const (
	// ProviderClaudeMax is the canonical vendor id. It is NOT "anthropic":
	// that id means the pay-per-token API key, which this must never touch.
	ProviderClaudeMax = "claude_max"

	// defaultClaudeMaxModel is what a turn runs on when Settings names
	// nothing. The full id rather than the "opus" alias so the model does not
	// change under him the day Anthropic repoints the alias: Opus 5 is what
	// Max runs by default, and that is what this should mean a year from now
	// too.
	defaultClaudeMaxModel = "claude-opus-5"
)

// BrainTurn is one conversational turn handed to the harness.
type BrainTurn struct {
	// SessionID is Infinity's session. It is the cache key: every turn of one
	// conversation resumes the SAME Claude Code session, which is what keeps
	// the subscription's one-hour prompt cache warm instead of re-reading the
	// whole history at full price on every message. Losing this is the single
	// most expensive mistake this file could make.
	SessionID string
	// Resume is the Claude Code session id to continue, or empty to start
	// cold. Resolved from SessionID by the provider before the call.
	Resume string
	// Prompt is what to say this turn. On a resumed session it is just the
	// boss's new message - Claude Code already holds everything before it.
	// On a cold start it carries the system prompt too.
	Prompt string
	Model  string
	Effort string
	// OnSession is called the moment Claude Code's own session id appears in
	// the stream, which is its very first line. It fires BEFORE the turn
	// finishes on purpose: a turn the boss interrupts, or one that dies with
	// the bridge, still leaves a resumable session behind, so his next message
	// continues the conversation instead of starting cold and cache-less.
	OnSession func(claudeSessionID string)
}

// BrainRunner is the seam to the Claude Code harness. The implementation lives
// in the tools package (it owns the bridge, the launch script, the
// subscription proof and the stream parser); llm must not import tools, so the
// contract lives here and serve.go injects the concrete runner. Same shape as
// the FrontierSampler seam, for the same reason.
type BrainRunner interface {
	// Converse runs one turn to completion, emitting progress on out. It
	// returns the finished reply plus the turn's token usage. The Claude Code
	// session id it ran under is reported through SessionSink so the next turn
	// can resume it.
	Converse(ctx context.Context, turn BrainTurn, out chan<- StreamEvent) (Response, error)
}

// BrainSessionStore is the durable Infinity-session to Claude-session mapping.
// Deliberately the shape of settings.Store's generic Get/Set over
// infinity_meta, so serve.go passes that store in with no adapter. In the
// database rather than a map in memory because a Core restart in the middle of
// a conversation would otherwise silently drop the boss back to a cold,
// uncached, context-free session and he would only notice by the answer being
// worse.
type BrainSessionStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// ClaudeCode is the Provider. It is a thin conversational shell over the
// harness: resolve the session, build the turn, stream it, remember the
// session id.
type ClaudeCode struct {
	runner   BrainRunner
	sessions BrainSessionStore
	model    string

	// mu guards warm, the in-process mirror of the session mapping. The store
	// is authoritative; this only saves a database round trip per turn.
	mu   sync.Mutex
	warm map[string]string
}

// NewClaudeCode builds the provider. A nil runner yields a STUB: it registers
// nowhere and reports itself unimplemented, so Settings shows "not connected"
// rather than offering a brain that cannot answer. That is the same contract
// the Google stub follows.
func NewClaudeCode(runner BrainRunner, sessions BrainSessionStore, model string) *ClaudeCode {
	if strings.TrimSpace(model) == "" {
		model = defaultClaudeMaxModel
	}
	return &ClaudeCode{
		runner:   runner,
		sessions: sessions,
		model:    model,
		warm:     map[string]string{},
	}
}

func (c *ClaudeCode) Name() string  { return ProviderClaudeMax }
func (c *ClaudeCode) Model() string { return c.model }

// Implemented reports false when there is no harness to run on, which keeps a
// dead brain out of the registry and out of the Settings picker.
func (c *ClaudeCode) Implemented() bool { return c != nil && c.runner != nil }

// Stream is the non-caching entry point. It renders the two system segments
// and defers to StreamCached, so the ~10 one-shot callers (summarizer, critic,
// namer) work unchanged.
func (c *ClaudeCode) Stream(ctx context.Context, model, system string, messages []Message, tools []ToolDef, out chan<- StreamEvent) (Response, error) {
	return c.StreamCached(ctx, model, SystemPrompt{Stable: system}, messages, tools, out)
}

// StreamCached runs one turn.
//
// The `tools` argument is intentionally unused. Claude Code brings its own
// tools and reaches Infinity's registry over MCP; passing our catalog in the
// prompt as well would describe every tool twice to a model that can already
// see them, and invite it to narrate calls it cannot make.
func (c *ClaudeCode) StreamCached(ctx context.Context, model string, sys SystemPrompt, messages []Message, _ []ToolDef, out chan<- StreamEvent) (Response, error) {
	if !c.Implemented() {
		return Response{}, fmt.Errorf("%w: Claude Max needs the Mac bridge, and it is not attached", ErrNotImplemented)
	}
	// The session id rides the context as the cache key - the same value the
	// OpenAI providers forward as prompt_cache_key. Here it does more than
	// route a shard: it is how we find the Claude Code session to resume.
	sessionID := CacheKeyFromContext(ctx)

	prompt, err := c.buildPrompt(ctx, sessionID, sys, messages)
	if err != nil {
		return Response{}, err
	}

	turn := BrainTurn{
		SessionID: sessionID,
		Resume:    c.resume(ctx, sessionID),
		Prompt:    prompt,
		Model:     firstNonEmpty(model, c.model),
		Effort:    string(EffortFromContext(ctx)),
		OnSession: func(id string) { c.RememberSession(ctx, sessionID, id) },
	}
	// A cold start has to carry the system prompt; a resumed session already
	// holds it and re-sending would both waste tokens and break the cached
	// prefix that resuming exists to preserve.
	if turn.Resume == "" {
		turn.Prompt = coldStartPrompt(sys, prompt)
	}

	resp, err := c.runner.Converse(ctx, turn, out)
	if err != nil {
		return resp, err
	}
	return resp, nil
}

// RememberSession records the Claude Code session id for an Infinity session.
// Called by the runner the moment the id appears in the stream (its first
// line), so even a turn that is interrupted mid-flight leaves a resumable
// handle behind rather than forcing the next message to start cold.
func (c *ClaudeCode) RememberSession(ctx context.Context, sessionID, claudeSessionID string) {
	if sessionID == "" || claudeSessionID == "" {
		return
	}
	c.mu.Lock()
	c.warm[sessionID] = claudeSessionID
	c.mu.Unlock()
	if c.sessions != nil {
		// Best effort: a failed write costs one cold start next turn, which
		// is not worth failing the boss's answer over.
		_ = c.sessions.Set(ctx, brainSessionKey(sessionID), claudeSessionID)
	}
}

// resume resolves the Claude Code session to continue, warm map first.
func (c *ClaudeCode) resume(ctx context.Context, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	c.mu.Lock()
	id, ok := c.warm[sessionID]
	c.mu.Unlock()
	if ok && id != "" {
		return id
	}
	if c.sessions == nil {
		return ""
	}
	stored, found, err := c.sessions.Get(ctx, brainSessionKey(sessionID))
	if err != nil || !found || strings.TrimSpace(stored) == "" {
		return ""
	}
	c.mu.Lock()
	c.warm[sessionID] = stored
	c.mu.Unlock()
	return stored
}

func brainSessionKey(sessionID string) string {
	return "claude_brain.session." + sessionID
}

// buildPrompt renders what to actually say this turn.
//
// On a resumed session that is the newest user message and nothing else:
// Claude Code is holding the conversation, and replaying it would defeat the
// resume. On a cold start we render the history so a session that begins
// mid-conversation (a Core restart, a brain switched in Settings partway
// through) does not lose what was already said.
func (c *ClaudeCode) buildPrompt(ctx context.Context, sessionID string, sys SystemPrompt, messages []Message) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("claude_max: nothing to say (no messages)")
	}
	if c.resume(ctx, sessionID) != "" {
		if last, ok := lastUserMessage(messages); ok {
			// The new message, with THIS turn's volatile context in front of
			// it: what RRF just retrieved, the current time, the account
			// overlay. Claude Code is holding the conversation and the soul,
			// but it cannot hold context that did not exist when the session
			// started, and a brain that stops seeing freshly recalled memory
			// after turn one is the amnesia this whole path exists to avoid.
			//
			// This costs nothing in cache terms and is exactly what every
			// other provider does: the cached prefix is what came before,
			// and new content appended after it never invalidates that.
			return withVolatile(sys, last), nil
		}
		// No trailing user message means the loop is continuing after a tool
		// result, which cannot happen on this provider (Claude Code runs its
		// own tools). Fall through to the full render rather than send an
		// empty prompt.
	}
	return renderTranscript(messages), nil
}

// withVolatile puts this turn's changing context in front of the message.
func withVolatile(sys SystemPrompt, message string) string {
	vol := strings.TrimSpace(sys.Volatile)
	if vol == "" {
		return message
	}
	return vol + "\n\n---\n\n" + message
}

// coldStartPrompt puts the system prompt in front of the first turn. Claude
// Code takes a prompt, not a system field, so the soul and the volatile
// context are rendered as a leading block that the session then carries for
// the rest of the conversation.
func coldStartPrompt(sys SystemPrompt, prompt string) string {
	rendered := sys.Render()
	if strings.TrimSpace(rendered) == "" {
		return prompt
	}
	return rendered + "\n\n---\n\n" + prompt
}

// lastUserMessage returns the newest user turn's text.
func lastUserMessage(messages []Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			text := strings.TrimSpace(messages[i].Content)
			if text == "" {
				return "", false
			}
			return text, true
		}
	}
	return "", false
}

// renderTranscript flattens a conversation into one prompt for a cold start.
func renderTranscript(messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case RoleUser:
			b.WriteString("Boss: ")
		case RoleAssistant:
			b.WriteString("You: ")
		default:
			continue
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}
