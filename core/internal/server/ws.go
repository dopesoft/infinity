package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func splitProto(h string) []string {
	parts := strings.Split(h, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func hasBearerPrefix(p string) bool { return strings.HasPrefix(p, "bearer.") }

type wsClientAttachment struct {
	Name        string `json:"name"`
	MimeType    string `json:"mime_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Text        string `json:"text,omitempty"`
	PreviewURL  string `json:"preview_url,omitempty"`
	StoragePath string `json:"storage_path,omitempty"`
}

type wsClientMessage struct {
	Type        string               `json:"type"`
	SessionID   string               `json:"session_id"`
	Content     string               `json:"content"`
	Attachments []wsClientAttachment `json:"attachments,omitempty"`
	// Voice marks a `message` frame whose content is a finalized voice
	// transcript. The turn runs the IDENTICAL Loop.Run as text (same brain,
	// memory, skills, tools, gate) and additionally streams the spoken reply
	// back as `voice_audio` frames. This is the whole of voice parity: voice
	// is just a text turn with a mic on the front and TTS on the back.
	Voice bool `json:"voice,omitempty"`
	// Effort is the boss's per-turn thinking-level override from the Composer
	// ThinkingChip: "" / "auto" = let steal C decide; or a level (none|low|
	// medium|high|xhigh). Stamped onto the turn ctx so the effort router honors
	// it ahead of any auto signal.
	Effort string `json:"effort,omitempty"`
}

type wsServerEvent struct {
	Type       string         `json:"type"`
	SessionID  string         `json:"session_id"`
	Text       string         `json:"text,omitempty"`
	Usage      map[string]int `json:"usage,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Message    string         `json:"message,omitempty"`
	ToolCall   *wsToolEvent   `json:"tool_call,omitempty"`
	ToolResult *wsToolEvent   `json:"tool_result,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
	Progress   *float32       `json:"progress,omitempty"`
	// ProgressStep/Action/Detail/Task enrich background_build_progress
	// frames so Studio can render a tool-call-sized card with the live
	// step number, the verb (edit/write/bash…), the current file or
	// command, and the originating task — instead of a bare "step N" line.
	ProgressStep   int    `json:"progress_step,omitempty"`
	ProgressAction string `json:"progress_action,omitempty"`
	ProgressDetail string `json:"progress_detail,omitempty"`
	ProgressTask   string `json:"progress_task,omitempty"`
	// Steered marks a delta/complete that resulted from a mid-turn steer
	// (used by the studio transcript to render the "↳ steered" badge on
	// reconstructed bubbles). Empty by default.
	Steered bool `json:"steered,omitempty"`
	// Intent carries the per-turn IntentFlow classification. Only present
	// on type="intent" frames. Studio's IntentStream panel reads this
	// directly; the chat transcript ignores it.
	Intent *wsIntent `json:"intent,omitempty"`
	// Gauge carries the per-turn effort sizing (glance/standard/deep). Only
	// present on type="gauge" frames. Studio renders it as a chip beside the
	// intent chip; the transcript ignores it.
	Gauge *wsGauge `json:"gauge,omitempty"`
	// Effort carries the per-turn reasoning level steal C chose for this turn.
	// Present on type="effort" frames; the Composer ThinkingChip renders it as
	// "Auto · <level>" so the boss sees how hard Jarvis is thinking.
	Effort *wsEffort `json:"effort,omitempty"`
	// FindingKind is set on type="proactive_message" frames so Studio can
	// render an icon + tone consistent with the Heartbeat tab - e.g.
	// "surprise" gets a lightbulb, "security" gets a shield.
	FindingKind string `json:"finding_kind,omitempty"`
	// CuriosityID is set on type="proactive_message" frames for findings
	// backed by a mem_curiosity_questions row, so the chat card can offer
	// an "Approve & fix" action that round-trips to the decide endpoint.
	CuriosityID string `json:"curiosity_id,omitempty"`
	// BrowserFrame is set on type="browser_frame" frames — a live CDP
	// screencast frame from the cloud browser, routed to the session's
	// Studio tab so the boss watches Jarvis drive in real time. This rides
	// the per-session broadcaster (sessionSender), NOT the turn's RunEvent
	// stream, so it streams continuously across every observe/act/extract
	// call for the whole browser session.
	BrowserFrame *wsBrowserFrame `json:"browser_frame,omitempty"`
	// BrowserControl is set on type="browser_control" frames — who is
	// driving the live browser (takeover coordination).
	BrowserControl *wsBrowserControl `json:"browser_control,omitempty"`
	// DocumentCreated fires when document_create produces a file, so Studio
	// opens a NEW tab with the rendered report + download. Rides the
	// per-session broadcaster like browser frames. Cloud-first: the markdown
	// rides the event (no fetch) and binaries download via the cloud-direct
	// /api/workspace/download proxy — works from any device regardless of the
	// session's Mac/Cloud bridge.
	DocumentCreated *wsDocumentCreated `json:"document_created,omitempty"`
	// ToolInputDelta streams the model writing a tool call's arguments live —
	// e.g. the file content for an edit — BEFORE the tool runs, so Studio opens
	// the file in the canvas and types it in as it's generated. Rides the
	// turn's RunEvent stream. Model-agnostic: providers that can't stream tool
	// args simply never emit these and the canvas falls back to opening the
	// file with the complete content from the tool_call event.
	ToolInputDelta *wsToolInputDelta `json:"tool_input_delta,omitempty"`
	// ProjectChanged fires when the session's active project switches (agent
	// called project_open/create/clone, or the boss switched it) so Studio
	// re-scopes the canvas instantly instead of waiting on the 1.5s poll.
	ProjectChanged *wsProjectChanged `json:"project_changed,omitempty"`
	// Audio carries one synthesized speech clip for a voice turn (one
	// sentence of Jarvis's spoken reply). Studio decodes + plays clips in
	// Seq order. Rides the turn's event stream so it stops cleanly on
	// interrupt/barge-in.
	Audio *wsVoiceAudio `json:"audio,omitempty"`
}

// wsEffort is the per-turn reasoning level chosen by steal C, surfaced so the
// Composer ThinkingChip can show "Auto · <level>". Source is the audit reason
// (boss_pinned / coding_floor / gauge_deep / high_surprise / call_rate / ...).
type wsEffort struct {
	Level  string `json:"level"`
	Source string `json:"source,omitempty"`
}

// wsVoiceAudio is one TTS clip. Data is base64-encoded audio bytes of MimeType
// (audio/mpeg). Seq orders clips within a turn so the browser plays them in the
// order Jarvis said them even if synthesis latency varies.
type wsVoiceAudio struct {
	Seq      int    `json:"seq"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

// wsProjectChanged tells Studio the session is now scoped to a different project
// (or back to Jarvis's own code when project_path is "").
type wsProjectChanged struct {
	ProjectPath string `json:"project_path"`
}

// wsToolInputDelta is one chunk of a tool call's arguments as the model writes
// them. ID/Name match the eventual tool_call so Studio can correlate; Delta is
// the raw partial-JSON argument fragment.
type wsToolInputDelta struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Delta string `json:"delta"`
}

// wsDocumentCreated tells Studio to open a generated document in a new tab.
type wsDocumentCreated struct {
	Format    string `json:"format"`
	Filename  string `json:"filename"`
	Path      string `json:"path"` // cloud workspace path (for download)
	Bytes     int64  `json:"bytes,omitempty"`
	Markdown  string `json:"markdown,omitempty"`   // rendered inline for md/report formats
	PDFPath   string `json:"pdf_path,omitempty"`   // sibling PDF for preview, when also_pdf
	ThumbPath string `json:"thumb_path,omitempty"` // page-1 PNG for the Artifacts/Media gallery
	HTMLPath  string `json:"html_path,omitempty"`  // side-scrollable HTML preview for spreadsheets
	ID        string `json:"id,omitempty"`         // mem_artifacts id (so Studio can dedupe vs the fetched list)
}

// wsBrowserFrame is one screencast frame for the Studio Preview pane (live browser).
// wsBrowserControl announces who is driving a live browser session -
// "agent" or "human" - so the Preview pane renders the takeover state.
type wsBrowserControl struct {
	BrowserSessionID string `json:"browser_session_id"`
	Controller       string `json:"controller"`
	Reason           string `json:"reason,omitempty"`
}

type wsBrowserFrame struct {
	Seq              int    `json:"seq"`
	Frame            string `json:"frame"`                        // data:image/jpeg;base64,...
	URL              string `json:"url,omitempty"`                // current page URL for the toolbar
	BrowserSessionID string `json:"browser_session_id,omitempty"` // for the Stop button + run status
}

type wsToolEvent struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input,omitempty"`
	Output    string         `json:"output,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	EndedAt   time.Time      `json:"ended_at,omitempty"`
	// Set on tool_call events when the gate is parking the call on a
	// Trust contract. Studio reads these to render the inline Approve/
	// Deny buttons inside the same tool card - without these fields
	// reaching the browser the card spins forever and the agent loop
	// silently times out on WaitForDecision.
	AwaitingApproval bool   `json:"awaiting_approval,omitempty"`
	ContractID       string `json:"contract_id,omitempty"`
	Preview          string `json:"preview,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
	// Echo the bearer subprotocol back so browsers that opted into it
	// don't fail the handshake (browsers reject upgrades whose response
	// drops the requested subprotocol entirely).
	Subprotocols: []string{},
}

func formatAttachmentsForPrompt(atts []wsClientAttachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nAttached files:\n")
	for i, att := range atts {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = "attachment"
		}
		fmt.Fprintf(&b, "%d. %s", i+1, name)
		meta := make([]string, 0, 2)
		if mt := strings.TrimSpace(att.MimeType); mt != "" {
			meta = append(meta, mt)
		}
		if att.SizeBytes > 0 {
			meta = append(meta, fmt.Sprintf("%d bytes", att.SizeBytes))
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(meta, ", "))
		}
		b.WriteString("\n")
		if text := strings.TrimSpace(att.Text); text != "" {
			b.WriteString("Contents:\n")
			b.WriteString(text)
			if !strings.HasSuffix(text, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func withAttachmentContext(content string, atts []wsClientAttachment) string {
	formatted := formatAttachmentsForPrompt(atts)
	if formatted == "" {
		return content
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return formatted
	}
	return trimmed + formatted
}

func attachmentPayload(atts []wsClientAttachment) []map[string]any {
	if len(atts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(atts))
	for _, att := range atts {
		row := map[string]any{}
		if v := strings.TrimSpace(att.Name); v != "" {
			row["name"] = v
		}
		if v := strings.TrimSpace(att.MimeType); v != "" {
			row["mime_type"] = v
		}
		if att.SizeBytes > 0 {
			row["size_bytes"] = att.SizeBytes
		}
		if v := strings.TrimSpace(att.Text); v != "" {
			row["text"] = v
		}
		if v := strings.TrimSpace(att.PreviewURL); v != "" {
			row["preview_url"] = v
		}
		if v := strings.TrimSpace(att.StoragePath); v != "" {
			row["storage_path"] = v
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func payloadWithAttachments(payload map[string]any, atts []wsClientAttachment) map[string]any {
	rows := attachmentPayload(atts)
	if len(rows) == 0 {
		return payload
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["attachments"] = rows
	return payload
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.loop == nil {
		http.Error(w, "agent loop not configured", http.StatusServiceUnavailable)
		return
	}

	// Authorize before upgrade so we can return a real 401 to the browser.
	// (After upgrade, the response is hijacked and any HTTP status we'd write
	// would never reach the client.)
	userID, err := s.auth.AuthorizeRequest(r)
	if err != nil {
		log.Printf("ws auth: %v", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Mirror back any bearer.<jwt> subprotocol the client sent so the
	// browser accepts the upgrade. Other subprotocols are ignored.
	var responseHeader http.Header
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		for _, p := range splitProto(proto) {
			if hasBearerPrefix(p) {
				responseHeader = http.Header{"Sec-WebSocket-Protocol": []string{p}}
				break
			}
		}
	}

	conn, err := upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	// connCtx is only for connection-scoped side work (WAL extraction,
	// intent classification). Agent turns deliberately do not inherit it;
	// startTurn detaches them so browser backgrounding or a reconnect
	// cannot cancel the response the boss is waiting on.
	connCtx, cancelConn := context.WithCancel(r.Context())
	defer cancelConn()

	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	writeMu := sync.Mutex{}
	send := func(ev wsServerEvent) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(ev); err != nil {
			log.Printf("ws write: %v", err)
		}
	}

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for range pingTicker.C {
			writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_ = conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
		}
	}()

	/* activeSessionID tracks the last session this connection hydrated, so
	 * we can unregister the right key on disconnect. Most browsers send the
	 * same sessionID for the lifetime of the tab; tab-swap pairs an unregister
	 * with a register on the next message. */
	var activeSessionID string
	defer func() {
		if activeSessionID != "" {
			s.unregisterSession(activeSessionID, send)
		}
	}()

	for {
		var msg wsClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) &&
				!websocket.IsUnexpectedCloseError(err) {
				log.Printf("ws read: %v", err)
			}
			return
		}

		switch msg.Type {
		case "ping":
			send(wsServerEvent{Type: "pong", SessionID: msg.SessionID})
			continue
		case "clear":
			s.loop.ClearSession(msg.SessionID)
			send(wsServerEvent{Type: "cleared", SessionID: msg.SessionID})
			continue
		case "interrupt":
			// Cancel any in-flight turn for this session. The turn's
			// runTurn goroutine will emit `complete{stop_reason:
			// "interrupted"}` once the LLM stream unwinds, so the
			// client sees a clean turn end (not an error).
			s.interruptTurn(msg.SessionID)
			continue
		case "steer":
			// Mid-turn user input. If a turn is in flight for this
			// session, the agent loop drains the steer channel between
			// iterations and appends the message as a fresh user turn.
			// If no turn is in flight, fall through to start a normal
			// turn so the client doesn't have to distinguish.
			if s.steerTurn(msg.SessionID, withAttachmentContext(msg.Content, msg.Attachments), send) {
				/* WAL the steer too - corrections often arrive as
				 * mid-turn nudges and we need them on the durable
				 * SESSION-STATE log just like a first message. */
				s.appendWAL(connCtx, msg.SessionID, msg.Content)
				continue
			}
			sessionID := msg.SessionID
			if sessionID == "" {
				sessionID = uuid.NewString()
			}
			if sessionID != activeSessionID {
				if activeSessionID != "" {
					s.unregisterSession(activeSessionID, send)
				}
				activeSessionID = sessionID
				s.registerSession(sessionID, send)
			}
			s.appendWAL(connCtx, sessionID, msg.Content)
			s.classifyIntentAsync(connCtx, sessionID, msg.Content, send)
			s.classifyGaugeAsync(connCtx, sessionID, msg.Content, send)
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, withAttachmentContext(msg.Content, msg.Attachments), msg.Voice, msg.Effort, send)
			continue
		case "message":
			sessionID := msg.SessionID
			if sessionID == "" {
				sessionID = uuid.NewString()
			}
			/* Register this connection under the session so the heartbeat
			 * broadcaster can target it on a proactive surface. Safe to
			 * call repeatedly - the latest send func wins. */
			if sessionID != activeSessionID {
				if activeSessionID != "" {
					s.unregisterSession(activeSessionID, send)
				}
				activeSessionID = sessionID
				s.registerSession(sessionID, send)
			}
			/* WAL: extract corrections / preferences / dates / decisions
			 * from the user message and persist to mem_session_state. Runs
			 * synchronously - regex over the message string only, no LLM. */
			s.appendWAL(connCtx, sessionID, msg.Content)
			/* IntentFlow: classify this turn in the background. The agent
			 * loop always runs regardless of the decision; the decision is
			 * recorded for analytics and emitted as an `intent` frame so
			 * Studio's IntentStream panel updates live. */
			s.classifyIntentAsync(connCtx, sessionID, msg.Content, send)
			s.classifyGaugeAsync(connCtx, sessionID, msg.Content, send)
			// Auto-route to steer when a turn is already running for
			// this session. This lets the studio compose+send while
			// streaming without having to switch message types - the
			// server figures it out.
			if s.steerTurn(sessionID, withAttachmentContext(msg.Content, msg.Attachments), send) {
				continue
			}
			// First message for this session since startup (or after
			// the agent restarted): preload prior turns from
			// mem_observations so the model sees the same conversation
			// the user does.
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, withAttachmentContext(msg.Content, msg.Attachments), msg.Voice, msg.Effort, send)
		case "resume":
			// Run one agent turn against a session's existing history
			// without a fresh user message. Discuss-with-Jarvis uses this:
			// the seeded DashboardSeed context block is the opening turn,
			// and Studio fires `resume` once on session open so the agent
			// actually replies to it (instead of the context just sitting
			// silent in the transcript).
			sessionID := msg.SessionID
			if sessionID == "" {
				send(wsServerEvent{Type: "error", SessionID: "", Message: "resume requires a session id"})
				continue
			}
			if sessionID != activeSessionID {
				if activeSessionID != "" {
					s.unregisterSession(activeSessionID, send)
				}
				activeSessionID = sessionID
				s.registerSession(sessionID, send)
			}
			// A turn already running for this session - nothing to do; the
			// in-flight turn will produce the reply. Prevents a double-fire
			// if Studio retries the resume across a reconnect.
			s.turnsMu.Lock()
			_, busy := s.turns[sessionID]
			s.turnsMu.Unlock()
			if busy {
				continue
			}
			s.hydrateLoopSession(r, sessionID)
			// Guard against resuming a session with no history at all -
			// the LLM stream would error on an empty message list. The
			// seeded-session path always has the DashboardSeed turn, so
			// this only trips on a misuse of the frame.
			if sess := s.loop.GetOrCreateSession(sessionID); sess == nil || len(sess.Snapshot()) == 0 {
				send(wsServerEvent{Type: "error", SessionID: sessionID, Message: "nothing to resume - session has no history"})
				continue
			}
			s.startTurn(connCtx, userID, sessionID, "", false, "", send)
		default:
			send(wsServerEvent{Type: "error", SessionID: msg.SessionID, Message: "unknown type: " + msg.Type})
		}
	}
}

// startTurn spawns a goroutine running one agent turn. It registers a
// cancel + steer channel in s.turns keyed by sessionID so subsequent WS
// frames can route to this turn (interrupt or steer) without blocking the
// reader. The goroutine's cleanup deregisters itself only if it's still
// the active state, preserving correctness across a cancel-then-new-turn
// sequence.
//
// Turn lifecycle is deliberately DECOUPLED from the WS connection: we
// use context.Background() as the parent instead of the connection's
// ctx, and route frames through s.sessionSender(sessionID) which looks
// up the live WS binding each emit. Result: if the boss switches apps
// on iOS Safari, navigates away in the browser, or the network flaps,
// the turn keeps running. Its assistant_text persists to mem_turns and
// becomes visible on reconnect via useChat's mergeServerRows refetch.
// The only thing that cancels a turn now is an explicit `interrupt`
// frame, the per-turn 5-minute timeout, or a new turn for the same
// session evicting it.
//
// interactiveTurnTimeout is the wall-clock ceiling for a single live-chat turn.
// The old 5-minute cap was too short for legitimate long work: a "scour the web
// and write me a report" turn does several SERVER-SIDE web searches (~20s each)
// plus reasoning and easily needs 10+ minutes — it was getting guillotined
// mid-work with an opaque "context deadline exceeded" (the 2026-07-02 report
// failures), which then also blocked the session from ever being auto-named
// (the namer only runs on a turn that completes). The Stop button
// (interruptTurn) and the LoopGate (50 calls / 5 min) remain the real backstops
// against a genuinely wedged turn, so a generous ceiling is safe. Override with
// INFINITY_TURN_TIMEOUT (Go duration, e.g. "20m").
func interactiveTurnTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("INFINITY_TURN_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}

// startTurn spawns the agent loop for one turn. The model is resolved
// server-side from the settings store (set by Studio's chip + Settings
// page) rather than carried on the WS frame - that way a single source
// of truth drives both the live chip and the Settings page, and a
// hostile client can't smuggle an arbitrary model id through the wire.
func (s *Server) startTurn(_ context.Context, userID, sessionID, content string, voiceTurn bool, effortPin string, _ func(wsServerEvent)) {
	// Use a fresh background context so the WS dying doesn't cancel this
	// turn. The interactiveTurnTimeout() below is the only deadline that applies.
	base := context.Background()
	// Resolve effective model from the persisted setting; empty string
	// means the agent loop falls back to the provider's boot default.
	model := s.resolveModel(base)
	// Attach the auth identity so any tool calls / hook fires that key
	// off the request user have it available. Then wrap in a per-turn
	// timeout so a wedged provider doesn't pin a goroutine forever.
	ctxWithUser := context.WithValue(base, auth.ContextKey{}, userID)
	// Voice turns carry a per-turn flag so the loop adds the spoken-delivery
	// overlay and runTurn streams TTS. Per-turn (not per-session) because
	// text + voice share the session id.
	if voiceTurn {
		ctxWithUser = agent.WithVoiceMode(ctxWithUser)
	}
	// steal C: carry the boss's per-turn thinking-level override into the turn.
	// "" / "auto" means let the effort router decide.
	if pin := strings.TrimSpace(effortPin); pin != "" && pin != "auto" {
		ctxWithUser = agent.WithEffortPin(ctxWithUser, pin)
	}
	turnCtx, cancel := context.WithTimeout(ctxWithUser, interactiveTurnTimeout())
	runContent, recovered := s.buildRecoveryPrompt(turnCtx, sessionID, content)
	// Route every WS frame through the live session binding rather than
	// the connection-bound closure the handler captured. See
	// sessionSender for the no-op-on-disconnect contract.
	send := s.sessionSender(sessionID)
	state := &turnState{
		cancel: cancel,
		steer:  make(chan string, 8),
	}

	s.turnsMu.Lock()
	if prev, ok := s.turns[sessionID]; ok {
		// Defensive: a prior turn should have been cleaned up. If it
		// somehow survived (panic in the goroutine before delete),
		// cancel it and overwrite - the new turn wins.
		prev.cancel()
	}
	s.turns[sessionID] = state
	s.turnsMu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.turnsMu.Lock()
			if cur, ok := s.turns[sessionID]; ok && cur == state {
				delete(s.turns, sessionID)
			}
			s.turnsMu.Unlock()
		}()
		s.runTurn(turnCtx, sessionID, runContent, model, voiceTurn, state.steer, send)
		if recovered && s.buffer != nil {
			_ = s.buffer.Clear(context.Background(), sessionID)
		}
	}()
}

// interruptTurn cancels the in-flight turn for the given session, if any.
// We remove the entry from the registry synchronously so a subsequent
// `message` doesn't race with the goroutine's cleanup and incorrectly
// route as a steer. The goroutine's deferred cleanup is idempotent.
func (s *Server) interruptTurn(sessionID string) {
	if sessionID == "" {
		return
	}
	s.turnsMu.Lock()
	state, ok := s.turns[sessionID]
	if ok {
		delete(s.turns, sessionID)
	}
	s.turnsMu.Unlock()
	if state != nil {
		state.cancel()
	}
}

// steerTurn routes a user-typed string into a running turn's steer channel.
// Returns true when the message was consumed by a turn (either queued or
// dropped with a soft error reported to the client). Returns false when no
// turn is in flight - the caller should start a fresh turn instead.
func (s *Server) steerTurn(sessionID, content string, send func(wsServerEvent)) bool {
	if sessionID == "" {
		return false
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	s.turnsMu.Lock()
	state, ok := s.turns[sessionID]
	s.turnsMu.Unlock()
	if !ok || state == nil {
		return false
	}
	select {
	case state.steer <- content:
		// Persist to mem_observations immediately so the message survives a
		// navigation/reload while the turn is still in flight. drainSteer
		// only appends to the in-memory session; the hook fires here so
		// fetchSessionMessages returns the steer without waiting for the next
		// iteration boundary (which can be minutes away when the LLM is
		// mid-stream or the loop is blocked inside WaitForDecision).
		if h := s.loop.Hooks(); h != nil {
			h.Emit("UserPromptSubmit", sessionID, "", content, map[string]any{"steered": true})
		}
		// Echo the steered message back so other tabs (and the
		// originating tab's reconnect path) can render it. The
		// originating tab already inserted it optimistically.
		send(wsServerEvent{
			Type:      "steer_received",
			SessionID: sessionID,
			Text:      content,
			Steered:   true,
		})
		return true
	default:
		// Buffer is sized for human typing cadence; overflow is rare
		// and recoverable (the user can resend). Surface it cleanly
		// rather than silently dropping.
		send(wsServerEvent{
			Type:      "error",
			SessionID: sessionID,
			Message:   "steer buffer full; please wait a moment and resend",
		})
		return true
	}
}

func sendRunEventToWS(send func(wsServerEvent), ev agent.RunEvent) {
	if send == nil {
		return
	}
	sessionID := tools.SessionForPublish(ev.SessionID)
	switch ev.Kind {
	case agent.EventDelta:
		send(wsServerEvent{Type: "delta", SessionID: sessionID, Text: ev.TextDelta})
	case agent.EventThinking:
		send(wsServerEvent{Type: "thinking", SessionID: sessionID, Text: ev.ThinkingDelta})
	case agent.EventToolCall:
		if ev.ToolCall != nil {
			// Forward the full ToolEvent including the gate's
			// awaiting_approval signal - without these fields the
			// browser never knows to render the inline Approve /
			// Deny buttons and the user watches the card spin
			// while the agent loop is blocked on WaitForDecision.
			send(wsServerEvent{
				Type:      "tool_call",
				SessionID: sessionID,
				ToolCall: &wsToolEvent{
					ID:               ev.ToolCall.ID,
					Name:             ev.ToolCall.Name,
					Input:            ev.ToolCall.Input,
					StartedAt:        ev.ToolCall.StartedAt,
					AwaitingApproval: ev.ToolCall.AwaitingApproval,
					ContractID:       ev.ToolCall.ContractID,
					Preview:          ev.ToolCall.Preview,
				},
			})
		}
	case agent.EventToolInputDelta:
		// Live tool-argument tokens -> Studio opens the file in the canvas
		// and types it in as the model writes it. Skip empty/idless chunks
		// (some providers send the name/id before any argument bytes).
		if ev.InputDelta != "" {
			send(wsServerEvent{
				Type:      "tool_input_delta",
				SessionID: sessionID,
				ToolInputDelta: &wsToolInputDelta{
					ID:    ev.ToolCallID,
					Name:  ev.ToolName,
					Delta: ev.InputDelta,
				},
			})
		}
	case agent.EventToolResult:
		if ev.ToolResult != nil {
			send(wsServerEvent{
				Type:      "tool_result",
				SessionID: sessionID,
				ToolResult: &wsToolEvent{
					ID:        ev.ToolResult.ID,
					Name:      ev.ToolResult.Name,
					Output:    ev.ToolResult.Output,
					IsError:   ev.ToolResult.IsError,
					StartedAt: ev.ToolResult.StartedAt,
					EndedAt:   ev.ToolResult.EndedAt,
				},
			})
		}
	case agent.EventComplete:
		usage := map[string]int{}
		if ev.Usage != nil {
			// input = FULL prompt size (uncached + cache reads/writes) so the
			// chat meter reflects true window fill on a cache hit; the
			// cache_read/cache_write split lets the modal show the caching
			// effect. Accurate for every model: non-caching brains report 0.
			usage["input"] = ev.Usage.PromptTokens()
			usage["output"] = ev.Usage.Output
			usage["cache_read"] = ev.Usage.CacheRead
			usage["cache_write"] = ev.Usage.CacheWrite
		}
		send(wsServerEvent{
			Type:       "complete",
			SessionID:  sessionID,
			Usage:      usage,
			StopReason: ev.StopReason,
		})
	case agent.EventEffort:
		send(wsServerEvent{Type: "effort", SessionID: sessionID, Effort: &wsEffort{Level: ev.EffortLevel, Source: ev.EffortSource}})
	case agent.EventError:
		send(wsServerEvent{Type: "error", SessionID: sessionID, Message: ev.Error})
	}
}

// runTurn drives one agent turn and pumps RunEvent → WS frames. The caller
// (startTurn) owns the cancel + steer channel via the turns registry; we
// receive the steer channel as a receive-only param so the agent loop can
// drain it between iterations. ctx is already wrapped with the per-turn
// 5-minute timeout, so we don't re-wrap it here.
func (s *Server) runTurn(ctx context.Context, sessionID, content, model string, voiceTurn bool, steer <-chan string, send func(wsServerEvent)) {
	events := make(chan agent.RunEvent, 128)
	done := make(chan struct{})

	go func() {
		defer close(done)
		if err := s.loop.Run(ctx, sessionID, content, model, steer, events); err != nil {
			send(wsServerEvent{Type: "error", SessionID: sessionID, Message: err.Error()})
		}
		close(events)
	}()

	// Voice turns speak the brain's streamed text via TTS. nil for text
	// turns (and voice turns when no Speaker is configured) - then this is
	// the exact text path, captions only.
	speak := s.newSpeakPump(ctx, sessionID, voiceTurn, send)

	/* Accumulate the assistant's streamed text so on EventComplete we can
	 * write the full user/assistant pair into the WorkingBuffer when the
	 * context window is past threshold. We only need text deltas - tool
	 * calls aren't mirrored into the buffer (they'd churn it on every
	 * iteration without adding recoverable content). */
	var assistantText strings.Builder

	for ev := range events {
		if ev.Kind == agent.EventDelta {
			assistantText.WriteString(ev.TextDelta)
			speak.onDelta(ev.TextDelta)
		}
		if ev.Kind == agent.EventToolCall && ev.ToolCall != nil {
			speak.onToolCall(ev.ToolCall.Name)
		}
		sendRunEventToWS(send, ev)
		if ev.Kind == agent.EventComplete {
			/* Mirror this exchange into mem_working_buffer iff the
			 * model's context window crossed the proactive threshold
			 * (default 0.6 of max). Heuristic ctx_max - provider
			 * interface doesn't expose context window, so we infer
			 * from the model id. Fail-open: any error here is silent
			 * because the turn already succeeded. */
			usedTokens := 0
			if ev.Usage != nil {
				// Full window fill (PromptTokens includes cache reads/writes),
				// not bare Input - else a cache-heavy turn underreports and the
				// working-buffer mirror fails to trip at the proactive threshold.
				usedTokens = ev.Usage.PromptTokens() + ev.Usage.Output
			}
			s.captureWorkingBuffer(ctx, ev.SessionID, content, assistantText.String(), usedTokens)
		}
	}

	// Speak the trailing partial sentence and wait for synthesis to drain so
	// the last clip ships before the turn ctx is cancelled. No-op for text.
	speak.finish()
	<-done
}
