package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// Successes go to stdout; stderr is reserved for real failures (Railway tags
// severity by stream).
var brainInfo = log.New(os.Stdout, "", log.LstdFlags)

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
	// MaxTurns caps the harness's own tool loop for this turn (rendered as
	// --max-turns). 0 = unset. Sized from the agent loop's per-segment tool
	// budget so a cron sweep cannot run the plan down for an hour on one
	// prompt.
	MaxTurns int
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

// BrainFilePlacer puts one of the boss's attachments on the box the brain is
// running on, and reports the path it landed at.
//
// It exists because Claude Code takes a PROMPT, not typed content blocks. Every
// other brain receives an image as a native image block; this one physically
// cannot. What it can do is open a file, and its Read tool renders an image the
// same way a vision model sees one. So the file goes to the box and the model
// is told where it is.
//
// Optional: a runner that does not implement it still gets text and PDF text,
// and an image is named honestly as one it could not be shown.
type BrainFilePlacer interface {
	PlaceFile(ctx context.Context, id, name string, data []byte) (path string, err error)
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
	warm map[string]brainHandle
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
		warm:     map[string]brainHandle{},
	}
}

// RunsOwnTools: Claude Code executes inside its own harness. See
// SelfExecutingProvider.
func (c *ClaudeCode) RunsOwnTools() bool { return true }

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

	// What this turn will have covered once it answers: the transcript it was
	// given, plus the reply it is about to add.
	covered := countConversation(messages) + 1

	turn := BrainTurn{
		SessionID: sessionID,
		Resume:    c.resume(ctx, sessionID, messages),
		Prompt:    prompt,
		Model:     firstNonEmpty(model, c.model),
		Effort:    string(EffortFromContext(ctx)),
		MaxTurns:  MaxTurnsFromContext(ctx),
		OnSession: func(id string) { c.RememberSession(ctx, sessionID, id, covered) },
	}
	// A cold start has to carry the system prompt; a resumed session already
	// holds it and re-sending would both waste tokens and break the cached
	// prefix that resuming exists to preserve.
	if turn.Resume == "" {
		turn.Prompt = coldStartPrompt(sys, prompt)
		// A cold start on a conversation with history is the expensive case
		// this file exists to avoid (the whole transcript re-rendered and
		// re-written at full price), so it is said out loud every time. For
		// weeks every turn was cold and nothing noticed: the init line that
		// carries the session id was being clamped before it was parsed.
		if prior := countConversation(messages); prior > 1 {
			brainInfo.Printf("claude_max: COLD start for session %s: %d prior messages rendered into the prompt (no resume handle)", sessionID, prior)
		}
	}

	resp, err := c.runner.Converse(ctx, turn, out)
	if err == nil && turn.Resume != "" {
		// The resumed session now covers this exchange too. Refreshed here,
		// not only when the init line is seen: a handle whose `covered`
		// stops advancing is refused two turns later by resume(), and every
		// turn after that starts cold.
		c.RememberSession(ctx, sessionID, turn.Resume, covered)
	}
	if err == nil || turn.Resume == "" {
		return resp, err
	}

	// A RESUMED turn failed, and the one thing that turn depended on which a
	// fresh one does not is the handle to an existing Claude session. So the
	// rule is not "recognise this error": it is that continuing something is
	// only ever an optimisation, and when it does not work we do the thing
	// that never needed it - start over, with the whole conversation rendered
	// in, and answer him.
	//
	// No error text is matched, deliberately. Matching a vendor's wording
	// means every new way for a session to go bad is a new dead end for him
	// until somebody adds a case for it. The only failures excluded are the
	// ones where a second attempt provably cannot help: he stopped the turn,
	// there is no box to run on, the plan is not signed in, or the plan is
	// spent. Those are answered the same way twice, so trying twice would
	// only make him wait longer to read the same sentence.
	if !worthRetryingFresh(ctx, err) {
		return resp, err
	}
	brainInfo.Printf("claude_max: resuming %s failed (%v); starting fresh with the full transcript", turn.Resume, err)
	c.ForgetSession(ctx, sessionID)
	fresh := turn
	fresh.Resume = ""
	fresh.Prompt = coldStartPrompt(sys, c.renderTranscript(ctx, messages))
	return c.runner.Converse(ctx, fresh, out)
}

// worthRetryingFresh reports whether a failed turn deserves a second attempt
// from scratch. It asks about the SITUATION, never about the error's wording.
func worthRetryingFresh(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false // he stopped it, or the turn ran out of time
	}
	// Marked by the runner as a verdict about the plan or the box rather than
	// about this turn: no bridge, not signed in, not on the subscription, out
	// of usage. See Unrecoverable.
	return !IsUnrecoverable(err)
}

// RememberSession records the Claude Code session id for an Infinity session.
// Called by the runner the moment the id appears in the stream (its first
// line), so even a turn that is interrupted mid-flight leaves a resumable
// handle behind rather than forcing the next message to start cold.
//
// covered is how much of the conversation that Claude session will have seen
// once this turn lands. See brainHandle: it is what lets the next turn tell a
// session that holds the whole thread from one that was overtaken while a
// different brain was answering.
func (c *ClaudeCode) RememberSession(ctx context.Context, sessionID, claudeSessionID string, covered int) {
	if sessionID == "" || claudeSessionID == "" {
		return
	}
	c.store(ctx, sessionID, brainHandle{ID: claudeSessionID, Covered: covered})
}

// ForgetSession drops the resume handle so the next turn starts cold, with
// the whole transcript rendered into it.
//
// Called when Claude Code says the session is gone ("No conversation found
// with session ID: …"), which happens for reasons that are none of the boss's
// business: the box the session lived on was replaced, the cloud container
// restarted, the CLI moved the handle. A conversation must never dead-end on
// that - it re-reads its history and carries on.
func (c *ClaudeCode) ForgetSession(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	delete(c.warm, sessionID)
	c.mu.Unlock()
	if c.sessions != nil {
		_ = c.sessions.Set(ctx, brainSessionKey(sessionID), "")
	}
}

func (c *ClaudeCode) store(ctx context.Context, sessionID string, h brainHandle) {
	c.mu.Lock()
	c.warm[sessionID] = h
	c.mu.Unlock()
	if c.sessions != nil {
		// A failed write is not worth failing his answer over, but it is not
		// nothing either: it costs a cold start, every turn, forever, and it
		// is invisible unless something says so. Silence here hid a rejected
		// key for as long as this path has existed.
		if err := c.sessions.Set(ctx, brainSessionKey(sessionID), h.encode()); err != nil {
			log.Printf("claude_max: could not remember the session handle (every turn will start cold): %v", err)
		}
	}
}

// brainHandle is what one conversation remembers about its Claude Code
// session: which session to resume, and how much of the transcript that
// session has actually seen.
//
// Covered exists because the boss switches brains mid-thread. Claude Code
// holds the conversation ITSELF, so a session started before he moved to
// another vendor and back has no idea what was said in between: resuming it
// would answer confidently out of a stale half of his thread. Comparing what
// it covered against the transcript catches that, and the turn starts cold
// with the whole conversation rendered into it instead.
type brainHandle struct {
	ID string `json:"id"`
	// Covered counts the user+assistant messages this Claude session will
	// have seen, including the reply it is about to give. Zero means unknown
	// (a handle written before this was recorded), which is trusted rather
	// than thrown away.
	Covered int `json:"covered"`
}

func (h brainHandle) encode() string {
	raw, err := json.Marshal(h)
	if err != nil {
		return h.ID
	}
	return string(raw)
}

// decodeBrainHandle reads either shape: the JSON written today, or the bare
// session id written before Covered existed.
func decodeBrainHandle(stored string) brainHandle {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return brainHandle{}
	}
	if stored[0] != '{' {
		return brainHandle{ID: stored}
	}
	var h brainHandle
	if json.Unmarshal([]byte(stored), &h) != nil {
		return brainHandle{}
	}
	return h
}

// handle resolves the conversation's stored session, warm map first.
func (c *ClaudeCode) handle(ctx context.Context, sessionID string) brainHandle {
	if sessionID == "" {
		return brainHandle{}
	}
	c.mu.Lock()
	h, ok := c.warm[sessionID]
	c.mu.Unlock()
	if ok && h.ID != "" {
		return h
	}
	if c.sessions == nil {
		return brainHandle{}
	}
	stored, found, err := c.sessions.Get(ctx, brainSessionKey(sessionID))
	if err != nil || !found {
		return brainHandle{}
	}
	h = decodeBrainHandle(stored)
	if h.ID == "" {
		return brainHandle{}
	}
	c.mu.Lock()
	c.warm[sessionID] = h
	c.mu.Unlock()
	return h
}

// resume resolves the Claude Code session to continue, or "" to start cold.
//
// It refuses a handle the conversation has outgrown. A Claude session that
// covered the thread up to message N can be resumed for message N+1 (and for
// a second message the boss sent before it answered), but a transcript that
// has grown further means somebody else answered in between - the brain was
// switched - and that session cannot see those turns.
func (c *ClaudeCode) resume(ctx context.Context, sessionID string, messages []Message) string {
	h := c.handle(ctx, sessionID)
	if h.ID == "" {
		return ""
	}
	if h.Covered > 0 && countConversation(messages) > h.Covered+2 {
		return ""
	}
	return h.ID
}

// countConversation counts the messages a Claude session would have seen:
// the same user/assistant lines renderTranscript writes, and nothing else.
func countConversation(messages []Message) int {
	n := 0
	for _, m := range messages {
		if (m.Role == RoleUser || m.Role == RoleAssistant) && strings.TrimSpace(m.Content) != "" {
			n++
		}
	}
	return n
}

// brainSessionKey names the conversation's stored Claude session.
//
// The "setting." prefix is REQUIRED: the store this writes through
// (settings.Store, over infinity_meta) refuses any key without it. Without the
// prefix every write was rejected, RememberSession swallowed the error as
// best-effort, and the handle was never persisted at all - so every
// conversation started COLD after a restart, re-reading the entire transcript
// instead of resuming a warm session. The boss saw it as "why is he so slow":
// a resumed turn finished in 33s where a cold one took 1m36s and up.
func brainSessionKey(sessionID string) string {
	return "setting.claude_brain.session." + sessionID
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
	if c.resume(ctx, sessionID, messages) != "" {
		if last, ok := c.trailingUserMessages(ctx, messages); ok {
			// The new message(s): a world-state update when something
			// changed since the session opened, then the boss's message with
			// THIS turn's context pinned to it (Message.Volatile: what RRF
			// just retrieved, the current time, the plan). Claude Code is
			// holding the conversation and the soul, but it cannot hold
			// context that did not exist when the session started, and a
			// brain that stops seeing freshly recalled memory after turn one
			// is the amnesia this whole path exists to avoid.
			//
			// The context rides AFTER the message, as it does on every other
			// brain, and only the per-turn part is sent: the stable overlays
			// (tool catalog, accounts, bridge) were sent once as world state
			// and live in the session. The old shape prepended the whole
			// 64K-char volatile block to every message, and it stayed in
			// Claude Code's transcript forever: 94% of one real session's
			// user text was repeated context, which is what drove it to a
			// 900K-token window.
			return withPerCallVolatile(sys, last), nil
		}
		// No trailing user message means the loop is continuing after a tool
		// result, which cannot happen on this provider (Claude Code runs its
		// own tools). Fall through to the full render rather than send an
		// empty prompt.
	}
	return withPerCallVolatile(sys, c.renderTranscript(ctx, messages)), nil
}

// withPerCallVolatile appends the residual per-call overlay (wind-down,
// voice) when the loop set one. Usually empty: the turn's context is pinned
// on the message itself.
func withPerCallVolatile(sys SystemPrompt, prompt string) string {
	vol := strings.TrimSpace(sys.Volatile)
	if vol == "" {
		return prompt
	}
	return prompt + "\n\n---\n\n" + vol
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

// lastUserMessage is the single-message form of trailingUserMessages, kept
// for the attachment tests that exercise the resumed-turn render path.
func (c *ClaudeCode) lastUserMessage(ctx context.Context, messages []Message) (string, bool) {
	return c.trailingUserMessages(ctx, messages)
}

// trailingUserMessages returns the text of the user messages that arrived
// since the brain last spoke: normally one (the boss's message), sometimes
// two (a world-state update the loop appended ahead of it). They are joined
// in order so the update is read before the request. false when the newest
// message is not the boss's, which on this brain cannot happen mid-turn.
func (c *ClaudeCode) trailingUserMessages(ctx context.Context, messages []Message) (string, bool) {
	start := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleUser {
			break
		}
		start = i
	}
	if start < 0 {
		return "", false
	}
	var parts []string
	for _, m := range messages[start:] {
		if text := c.userContent(ctx, m); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

// userContent renders one of the boss's messages: what he typed, plus whatever
// he attached to it.
//
// THE FILE THAT NEVER ARRIVED (2026-09-01). He attached his resume, the chip
// rendered on his message, the upload landed, the PDF extracted cleanly to
// 12,163 characters, and the turn recorded it. Then Jarvis told him "no
// attachment came through on this message" - and from where he was sitting
// that was true, because this provider rendered `m.Content` and nothing else.
// Every other brain reads `m.Attachments`; this one, the one he is actually
// on, dropped them on the floor. His answer: "file uploads absolutely need to
// work".
//
// Claude Code takes a prompt, not typed content blocks, so the carrier is
// Attachment.TextBlock - the provider-neutral rendering that exists for
// exactly this ("documents on brains without native PDF input"). It is not
// prompt-stuffed METADATA, which is the thing this codebase forbids: it is the
// file's real extracted text, named, so he can be asked about it and quote it.
//
// Used by BOTH render paths (the resumed turn and the full transcript) so a
// resumed session cannot quietly be the one that loses his files.
func (c *ClaudeCode) userContent(ctx context.Context, m Message) string {
	text := strings.TrimSpace(m.Content)
	var b strings.Builder
	b.WriteString(text)
	for _, a := range m.Attachments {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(c.placed(ctx, a).TextBlock())
	}
	// The turn's pinned context trails the message it belongs to, the same
	// shape every other brain renders (Message.Volatile).
	if v := m.VolatileBlock(); v != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(v)
	}
	return strings.TrimSpace(b.String())
}

// placed puts the file where the model can open it, and says so.
//
// THE HALF THAT WAS MISSING. Rendering the extracted text made PDFs work and
// left images exactly as broken as before: named, and unseeable. The boss, and
// he should not have had to say it twice: "i need every fuckin model i use on
// mac or cloud bridge to take fuckin image or files, ALWAYS".
//
// So the bytes go to the box the brain runs on, and Path names them there.
// TextBlock already prints that path, and the note tells the model to open it,
// because a path it does not know to read is the same as no path. Its Read
// tool renders an image, so this is not a description of a picture - it is the
// picture.
//
// Failure is stated, never swallowed: if the file cannot be placed, the note
// says so and the boss gets told his image did not make it rather than an
// answer confidently written about something nobody looked at.
func (c *ClaudeCode) placed(ctx context.Context, a Attachment) Attachment {
	// Nothing to place: plain text rides in the block as text.
	if len(a.Data) == 0 {
		return a
	}
	// A document whose text came out clean needs no file: the text IS the
	// content, and it is already in the block. An image always needs the file,
	// and so does a PDF whose extraction failed or came back empty.
	if a.Kind == AttachmentDocument && strings.TrimSpace(a.Text) != "" {
		return a
	}
	placer, ok := c.runner.(BrainFilePlacer)
	if !ok {
		if a.Kind == AttachmentImage {
			a.Note = joinNote(a.Note, "this file could not be put on the machine you are running on, so you cannot open it — tell the boss it did not reach you rather than guessing at what it shows")
		}
		return a
	}
	path, err := placer.PlaceFile(ctx, a.ID, a.Name, a.Data)
	if err != nil || strings.TrimSpace(path) == "" {
		reason := "the machine you are running on could not be reached"
		if err != nil {
			reason = err.Error()
		}
		a.Note = joinNote(a.Note, "this file did not make it onto your machine ("+reason+"), so you cannot open it — tell the boss it did not reach you rather than guessing at its contents")
		return a
	}
	a.Path = path
	a.Note = joinNote(a.Note, "this file is on the machine you are running on at "+path+" — open it with your Read tool to see it")
	return a
}

// joinNote appends to a note without losing the one already there: an
// extraction failure the boss needs to hear about must not be overwritten by
// a delivery note.
func joinNote(existing, add string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return add
	}
	return existing + "; " + add
}

// renderTranscript flattens a conversation into one prompt for a cold start.
func (c *ClaudeCode) renderTranscript(ctx context.Context, messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		// A user message carries his attachments too; anything else is text.
		text := strings.TrimSpace(m.Content)
		if m.Role == RoleUser {
			text = c.userContent(ctx, m)
		}
		if text == "" {
			continue
		}
		switch {
		case IsWorldState(m):
			// Context, not his words: no speaker label.
		case m.Role == RoleUser:
			b.WriteString("Boss: ")
		case m.Role == RoleAssistant:
			b.WriteString("You: ")
		default:
			continue
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}
