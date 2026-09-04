package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/turnctx"
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
	// ID is the mem_attachments row Studio got back from
	// POST /api/attachments/upload. The bytes never ride the WS frame.
	ID          string `json:"id,omitempty"`
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
	// SinceSeq rides an `attach` frame: the newest seq this client has seen
	// for the session, so the journal replays only what it missed. Zero on a
	// cold open.
	SinceSeq uint64 `json:"since_seq,omitempty"`
	// ClientID is the browser's own id for the user message on a `message`
	// or `steer` frame. It is persisted on the UserPromptSubmit row and
	// echoed on steer_received, so the bubble already on screen is matched
	// by id when the transcript comes back, never by comparing text.
	ClientID string `json:"client_id,omitempty"`
}

type wsServerEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	// Seq / TurnID / Replay are the turn journal's stamps (turn_journal.go).
	// Seq is per-session monotonic across turns; every journaled frame has
	// one, so a client can tell a gap from a duplicate. Replay marks a frame
	// re-sent in answer to an attach - never dropped by the socket writer.
	Seq    uint64 `json:"seq,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
	Replay bool   `json:"replay,omitempty"`
	// MsgIndex, on a delta: which assistant message of the turn the text
	// belongs to. The persisted row carries the same number, so the browser
	// pairs the bubble with its row by (turn_id, msg_index).
	MsgIndex *int `json:"msg_index,omitempty"`
	// ClientID, on steer_received: the browser's id for the echoed message.
	ClientID string `json:"client_id,omitempty"`
	// TurnStatus is the payload of `turn_status` and `heartbeat` frames.
	TurnStatus *wsTurnStatus  `json:"turn_status,omitempty"`
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
	// ThinkingTokens rides a type="thinking" frame from a brain that reports
	// how MUCH it is reasoning rather than what (Claude Code redacts the
	// text). It is the only live evidence such a turn is alive, so the row
	// shows it instead of an empty box under a clock.
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
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
	// PhoneLive is set on type="phone_live" frames — one streamed
	// transcript line from a call in flight, or the final done event
	// carrying the outcome summary. Broadcast to every tab (the Phone
	// card lives on the dashboard, not in a chat session).
	PhoneLive *wsPhoneLive `json:"phone_live,omitempty"`
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

// wsPhoneLive mirrors phone.LiveEvent (mirrored, not imported, to keep the
// wire types self-contained in this file like the other ws* structs).
type wsPhoneLive struct {
	CallID    string `json:"call_id"`
	Direction string `json:"direction"`
	Number    string `json:"number,omitempty"`
	Name      string `json:"name,omitempty"`
	Speaker   string `json:"speaker,omitempty"`
	Text      string `json:"text,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status,omitempty"`
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
	// Nested marks a step taken INSIDE a coding job rather than by the chat
	// brain in this turn. Those keep arriving after the reply has landed — a
	// detached build runs for another forty minutes — so the browser must not
	// read one as "the turn is still going" (see BroadcastNestedStep).
	Nested bool `json:"nested,omitempty"`
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

// resolveAttachments turns the client's attachment refs into the typed
// llm.Attachment set the loop ships to the brain. Uploaded files (id) are
// loaded from the store with their bytes, extracted text and rasterized
// pages; legacy inline-text rows (no id) become text attachments. A ref that
// cannot be resolved becomes a loud Note attachment, never a silent omission.
func (s *Server) resolveAttachments(ctx context.Context, atts []wsClientAttachment) []llm.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]llm.Attachment, 0, len(atts))
	var ids []string
	for _, att := range atts {
		if id := strings.TrimSpace(att.ID); id != "" {
			ids = append(ids, id)
			continue
		}
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = "attachment"
		}
		a := llm.Attachment{Name: name, MIME: strings.TrimSpace(att.MimeType), Kind: llm.AttachmentText, SizeBytes: att.SizeBytes}
		if text := strings.TrimSpace(att.Text); text != "" {
			a.Text = text
		} else {
			a.Note = "this file was referenced without an upload id and none of its content reached me; ask the boss to re-attach it"
		}
		out = append(out, a)
	}
	if len(ids) > 0 {
		if s.attachments == nil {
			for _, id := range ids {
				out = append(out, llm.Attachment{ID: id, Name: "attachment " + id, Kind: llm.AttachmentText,
					Note: "the attachment store is not configured on this Core, so the file could not be loaded"})
			}
		} else {
			out = append(out, s.attachments.ToLLMMany(ctx, ids)...)
		}
	}
	return out
}

// turnText is the user text that opens (or steers) a turn. A file-only send
// still needs a non-empty message, because an empty userMsg is the loop's
// "resume" path; the marker just names the files.
func turnText(content string, atts []llm.Attachment) string {
	t := strings.TrimSpace(content)
	if t != "" || len(atts) == 0 {
		return t
	}
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		names = append(names, a.Name)
	}
	return "(attached: " + strings.Join(names, ", ") + ")"
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.loop == nil && s.runLoop == nil {
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

	// One writer goroutine per socket; producers never block on it and the
	// protocol ping lives in the same loop (ws_conn.go).
	c := newWSConn(conn)
	go c.writeLoop()
	defer c.close()
	send := c.send
	connID := c.id

	/* activeSessionID tracks the session this connection is bound to, so we
	 * can unregister the right key on disconnect. Binding happens on
	 * `attach` (every open and reconnect, every session switch) and, for
	 * older clients that never send one, on the first message/steer/resume. */
	var activeSessionID string
	bind := func(sessionID string) {
		if sessionID == "" || sessionID == activeSessionID {
			return
		}
		if activeSessionID != "" {
			s.unregisterSession(activeSessionID, connID)
		}
		activeSessionID = sessionID
		s.registerSession(sessionID, connID, send)
	}
	defer func() {
		if activeSessionID != "" {
			s.unregisterSession(activeSessionID, connID)
		}
	}()

	/* Broadcast registration, separate from session registration and done the
	 * moment the socket opens: the live call transcript has to reach the
	 * dashboard, which is the one tab that never chats. */
	s.registerBroadcast(connID, send)
	defer s.unregisterBroadcast(connID)

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
		case "attach":
			// The client binding itself to the chat it is looking at, and
			// asking what it missed. Answered with the server's own view of
			// the turn (turn_status) and then every journaled frame past
			// since_seq, flagged replay. This is what makes a reconnect
			// mid-turn invisible to the boss: the socket that arrives late
			// is caught up instead of left to guess.
			if msg.SessionID == "" {
				send(wsServerEvent{Type: "error", SessionID: "", Message: "attach requires a session id"})
				continue
			}
			bind(msg.SessionID)
			st, frames := s.attachSnapshot(msg.SessionID, msg.SinceSeq)
			send(wsServerEvent{Type: "turn_status", SessionID: msg.SessionID, TurnID: st.TurnID, Seq: st.Seq, TurnStatus: &st})
			for _, f := range frames {
				send(f)
			}
			continue
		case "clear":
			s.loop.ClearSession(msg.SessionID)
			send(wsServerEvent{Type: "cleared", SessionID: msg.SessionID})
			continue
		case "interrupt":
			// Cancel any in-flight turn for this session. The turn's
			// runTurn goroutine will emit `complete{stop_reason:
			// "interrupted"}` once the LLM stream unwinds, so the
			// client sees a clean turn end (not an error). When nothing is
			// running the answer is still an answer: a turn_status saying
			// so, so Stop can never be pressed into silence.
			if !s.interruptTurn(msg.SessionID) {
				st := s.turnStatusFor(msg.SessionID)
				send(wsServerEvent{Type: "turn_status", SessionID: msg.SessionID, TurnID: st.TurnID, Seq: st.Seq, TurnStatus: &st})
			}
			continue
		case "voice_interrupt":
			// Barge-in: the boss is talking over Jarvis. NOT a turn cancel -
			// the turn keeps running (his utterance arrives as a steer) - but
			// the mouth goes quiet server-side: without this, Core keeps
			// synthesizing + shipping the rest of the interrupted reply and
			// Jarvis "keeps jabbing on" after the client's local audio cut.
			// Speech resumes on EventSteered (see runTurn).
			s.turnsMu.Lock()
			if st, ok := s.turns[msg.SessionID]; ok && st != nil {
				st.speak.Squelch()
			}
			s.turnsMu.Unlock()
			continue
		case "steer":
			// Mid-turn user input. If a turn is in flight for this
			// session, the agent loop drains the steer channel between
			// iterations and appends the message as a fresh user turn.
			// If no turn is in flight, fall through to start a normal
			// turn so the client doesn't have to distinguish.
			steerAtts := s.resolveAttachments(connCtx, msg.Attachments)
			steerText := turnText(msg.Content, steerAtts)
			// Bind BEFORE routing. A steer is the first frame a reconnected
			// client sends when a turn is running, and binding after the
			// steer was consumed left that socket unbound - the turn streamed
			// into the void until the boss typed a fresh message.
			bind(msg.SessionID)
			// A mid-turn message re-reads the stance too ("ok, go ahead" flips
			// a discussion into work without a new turn). The re-read can only
			// ESCALATE: once the turn has run a work tool the holder refuses a
			// demotion back to discuss, so chatting to Jarvis mid-build no
			// longer retroactively shuts the consent gate on work he already
			// approved (turnctx.StanceHolder).
			if st := s.stanceFor(msg.SessionID); st != nil {
				s.classifyIntentAsync(connCtx, msg.SessionID, msg.Content, s.sessionSender(msg.SessionID), st, s.recentContextFn(msg.SessionID))
			}
			if s.steerTurn(msg.SessionID, steerText, steerAtts, msg.ClientID) {
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
			bind(sessionID)
			s.appendWAL(connCtx, sessionID, msg.Content)
			stance := s.stanceFor(sessionID)
			recent := s.recentContextFn(sessionID)
			s.classifyIntentAsync(connCtx, sessionID, msg.Content, s.sessionSender(sessionID), stance, recent)
			s.classifyGaugeAsync(connCtx, sessionID, msg.Content, s.sessionSender(sessionID), recent)
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, steerText, steerAtts, msg.Voice, msg.Effort, stance, msg.ClientID)
			continue
		case "message":
			sessionID := msg.SessionID
			if sessionID == "" {
				sessionID = uuid.NewString()
			}
			/* Bind this connection to the session so the turn's frames and
			 * the heartbeat broadcaster can reach it. Safe to call repeatedly. */
			bind(sessionID)
			/* WAL: extract corrections / preferences / dates / decisions
			 * from the user message and persist to mem_session_state. Runs
			 * synchronously - regex over the message string only, no LLM. */
			s.appendWAL(connCtx, sessionID, msg.Content)
			/* IntentFlow: classify this turn in the background. The agent
			 * loop always runs regardless of the decision; the decision is
			 * recorded for analytics and emitted as an `intent` frame so
			 * Studio's IntentStream panel updates live. Its stance (discuss /
			 * work) rides the turn: the loop holds work tools while the boss is
			 * talking it through. If a turn is already in flight the reading
			 * updates THAT turn's stance (the message becomes a steer). The
			 * frames go through the session binding, never this socket, so a
			 * reading that lands after a reconnect reaches the live tab. */
			stance := s.stanceFor(sessionID)
			recent := s.recentContextFn(sessionID)
			s.classifyIntentAsync(connCtx, sessionID, msg.Content, s.sessionSender(sessionID), stance, recent)
			s.classifyGaugeAsync(connCtx, sessionID, msg.Content, s.sessionSender(sessionID), recent)
			// Auto-route to steer when a turn is already running for
			// this session. This lets the studio compose+send while
			// streaming without having to switch message types - the
			// server figures it out.
			// Resolve uploads into typed blocks BEFORE routing, so a file
			// dropped mid-turn lands in the running turn exactly like text.
			msgAtts := s.resolveAttachments(connCtx, msg.Attachments)
			msgText := turnText(msg.Content, msgAtts)
			if s.steerTurn(sessionID, msgText, msgAtts, msg.ClientID) {
				continue
			}
			// First message for this session since startup (or after
			// the agent restarted): preload prior turns from
			// mem_observations so the model sees the same conversation
			// the user does.
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, msgText, msgAtts, msg.Voice, msg.Effort, stance, msg.ClientID)
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
			bind(sessionID)
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
			s.startTurn(connCtx, userID, sessionID, "", nil, false, "", nil, "")
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
// The only things that cancel a turn now are an explicit `interrupt`
// frame, the turn budget (turn_budget.go: a stall while the brain should
// be talking, or the absolute ceiling), or a new turn for the same session
// evicting it.

// startTurn spawns the agent loop for one turn. The model is resolved
// server-side from the settings store (set by Studio's chip + Settings
// page) rather than carried on the WS frame - that way a single source
// of truth drives both the live chip and the Settings page, and a
// hostile client can't smuggle an arbitrary model id through the wire.
func (s *Server) startTurn(_ context.Context, userID, sessionID, content string, atts []llm.Attachment, voiceTurn bool, effortPin string, stance *turnctx.StanceHolder, clientID string) {
	// Use a fresh background context so the WS dying doesn't cancel this
	// turn. The turn budget (guardTurn below) is the only deadline that applies.
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
	// Files attached to the opening message ride the ctx; the loop appends
	// them to the user llm.Message and persists their metadata on the hook.
	ctxWithUser = agent.WithAttachments(ctxWithUser, atts)
	// The consent read (discuss / work) rides the turn; nil on resume turns,
	// which the loop treats as unrestricted.
	if stance == nil {
		stance = turnctx.NewStanceHolder()
		stance.Set(turnctx.StanceUnknown, "resume turn")
	}
	ctxWithUser = turnctx.WithStance(ctxWithUser, stance)
	// ONE turn id. It is minted here, stamped on every frame by the journal,
	// and handed to the loop on the ctx so the mem_turns row and every
	// persisted assistant row carry the same id: that is what lets the
	// browser pair a live bubble with its transcript row by identity.
	turnID := uuid.NewString()
	ctxWithUser = turnctx.WithTurnID(ctxWithUser, turnID)
	// The browser's id for the message that opened the turn, for the same
	// reason: the user bubble is matched by id, not by text.
	ctxWithUser = turnctx.WithClientMessageID(ctxWithUser, clientID)
	// The turn's frames go through the journal FIRST and then to whatever
	// sockets are bound to the session at that moment (turn_journal.go). The
	// journal opens BEFORE the goroutine starts so an attach racing the start
	// already sees in_flight:true, and before the budget guard so the guard
	// has a clock to read.
	journal := s.journalFor(sessionID)
	journal.begin(turnID, model)
	turnCtx, cancel := guardTurn(ctxWithUser, journal, turnBudgetFromEnv())
	runContent, recovered := s.buildRecoveryPrompt(turnCtx, sessionID, content)
	send := s.turnSender(sessionID, journal)
	state := &turnState{
		cancel:  cancel,
		steer:   make(chan agent.Steer, 8),
		stance:  stance,
		journal: journal,
		// Voice turns get their speak pump minted here (not inside runTurn)
		// so it's reachable from the turns registry - that's what lets a
		// `voice_interrupt` frame squelch synthesis mid-reply. nil for text
		// turns and when no Speaker is configured.
		speak: s.newSpeakPump(turnCtx, sessionID, voiceTurn, send),
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
		// The visible pulse: a heartbeat every few seconds for as long as
		// the turn runs, so a brain thinking for three minutes never looks
		// dead. Stopped the moment the turn is over, and the journal closes
		// AFTER the last frame so an attach can never see in_flight:false
		// with the completion still on its way.
		stopPulse := make(chan struct{})
		go s.pulse(sessionID, journal, stopPulse)
		stopReason := s.runTurn(turnCtx, sessionID, runContent, model, state.speak, state.steer, send)
		close(stopPulse)
		journal.end(stopReason)
		if recovered && s.buffer != nil {
			_ = s.buffer.Clear(context.Background(), sessionID)
		}
	}()
}

// interruptTurn cancels the in-flight turn for the given session. It reports
// whether there was one to cancel, so the caller can answer a Stop that had
// nothing to stop instead of leaving the boss's tap unanswered.
//
// We remove the entry from the registry synchronously so a subsequent
// `message` doesn't race with the goroutine's cleanup and incorrectly route
// as a steer. The goroutine's deferred cleanup is idempotent.
//
// Stop means stop: any tool the Claude Code brain is running through the MCP
// endpoint for this session is cancelled too (mcp_server.go). That is the one
// path that still kills a brain-launched coding job, and it is the boss's
// own hand on the button.
func (s *Server) interruptTurn(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	s.turnsMu.Lock()
	state, ok := s.turns[sessionID]
	if ok {
		delete(s.turns, sessionID)
	}
	s.turnsMu.Unlock()
	s.cancelMCPExecs(sessionID)
	if state == nil {
		return false
	}
	if state.journal != nil {
		state.journal.stopping()
	}
	state.cancel()
	return true
}

// attachSnapshot answers an attach: the turn's status plus every journaled
// frame past sinceSeq. Taken as one snapshot so the count on the status
// matches the frames that follow it.
func (s *Server) attachSnapshot(sessionID string, sinceSeq uint64) (wsTurnStatus, []wsServerEvent) {
	j := s.journalPeek(tools.SessionForPublish(sessionID))
	if j == nil {
		return wsTurnStatus{}, nil
	}
	frames, truncated := j.since(sinceSeq)
	st := j.status()
	st.Replayed = len(frames)
	if truncated && len(frames) > 0 {
		// The client's since_seq predates the ring: it can have the tail,
		// but the head is only in the database. oldest_seq tells it so.
		st.OldestSeq = frames[0].Seq
	}
	return st, frames
}

// steerTurn routes a user-typed string into a running turn's steer channel.
// Returns true when the message was consumed by a turn (either queued or
// dropped with a soft error reported to the client). Returns false when no
// turn is in flight - the caller should start a fresh turn instead.
func (s *Server) steerTurn(sessionID, content string, atts []llm.Attachment, clientID string) bool {
	if sessionID == "" {
		return false
	}
	content = strings.TrimSpace(content)
	if content == "" && len(atts) == 0 {
		return false
	}
	s.turnsMu.Lock()
	state, ok := s.turns[sessionID]
	s.turnsMu.Unlock()
	if !ok || state == nil {
		return false
	}
	select {
	case state.steer <- agent.Steer{Text: content, Attachments: atts}:
		// Persist to mem_observations immediately so the message survives a
		// navigation/reload while the turn is still in flight. drainSteer
		// only appends to the in-memory session; the hook fires here so
		// fetchSessionMessages returns the steer without waiting for the next
		// iteration boundary (which can be minutes away when the LLM is
		// mid-stream or the loop is blocked inside WaitForDecision).
		if h := s.loop.Hooks(); h != nil {
			payload := map[string]any{"steered": true}
			if meta := llm.AttachmentsMeta(atts); len(meta) > 0 {
				payload["attachments"] = meta
			}
			if clientID != "" {
				payload["client_id"] = clientID
			}
			// The steer belongs to the running turn: stamp its id so the
			// transcript row joins the turn like every other row of it.
			if state.journal != nil {
				if tid := state.journal.status().TurnID; tid != "" {
					payload["turn_id"] = tid
				}
			}
			h.Emit("UserPromptSubmit", sessionID, "", content, payload)
		}
		// Echo the steered message back so every tab on the session (and a
		// socket that re-attaches later - it is journaled) can render it.
		// The originating tab already inserted it optimistically and matches
		// the echo by client_id.
		s.publish(sessionID, wsServerEvent{
			Type:      "steer_received",
			SessionID: sessionID,
			Text:      content,
			Steered:   true,
			ClientID:  clientID,
		})
		return true
	default:
		// Buffer is sized for human typing cadence; overflow is rare
		// and recoverable (the user can resend). Surface it cleanly
		// rather than silently dropping.
		s.sessionSender(sessionID)(wsServerEvent{
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
		idx := ev.MsgIndex
		send(wsServerEvent{Type: "delta", SessionID: sessionID, Text: ev.TextDelta, MsgIndex: &idx})
	case agent.EventThinking:
		send(wsServerEvent{
			Type: "thinking", SessionID: sessionID, Text: ev.ThinkingDelta,
			ThinkingTokens: ev.ThinkingTokens,
		})
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
// timeout, so we don't re-wrap it here.
//
// It returns how the turn ended, and it guarantees EXACTLY ONE terminal frame
// per turn: `complete` or `error`. Two exits used to end without either (the
// iteration cap, and a Loop.Run error after the events had drained), and a
// turn that never says it is over is a turn the browser cannot settle.
func (s *Server) runTurn(ctx context.Context, sessionID, content, model string, speak *speakPump, steer <-chan agent.Steer, send func(wsServerEvent)) string {
	events := make(chan agent.RunEvent, 128)
	done := make(chan struct{})
	var runErr error

	run := s.loopRunner()
	go func() {
		defer close(done)
		runErr = run(ctx, sessionID, content, model, steer, events)
		close(events)
	}()
	journal := s.journalPeek(sessionID)
	sawTerminal := false
	stopReason := ""

	// speak is the voice pump minted in startTurn (nil for text turns and
	// voice turns without a Speaker configured - then this is the exact text
	// path, captions only). It lives on the turnState so barge-in frames can
	// squelch it from the WS read loop.

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
		if ev.Kind == agent.EventSteered {
			// The loop absorbed the boss's mid-turn interjection - whatever
			// streams next answers it, so the mouth comes back on after a
			// barge-in squelch.
			speak.Unsquelch()
		}
		if journal != nil {
			journal.setPhase(ev)
		}
		switch ev.Kind {
		case agent.EventComplete:
			sawTerminal = true
			stopReason = ev.StopReason
			if stopReason == "" {
				stopReason = "end_turn"
			}
		case agent.EventError:
			sawTerminal = true
			stopReason = "error"
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
	if runErr != nil {
		// A Loop.Run error is terminal in its own right; it lands AFTER the
		// events it explains, never interleaved with them.
		if !sawTerminal {
			send(wsServerEvent{Type: "error", SessionID: sessionID, Message: runErr.Error()})
		}
		return "error"
	}
	if !sawTerminal {
		// The loop returned without saying it was done. The browser must
		// still hear that the turn is over, or it spins on a turn nobody is
		// running.
		send(wsServerEvent{Type: "complete", SessionID: sessionID, StopReason: "ended"})
		return "ended"
	}
	return stopReason
}

// loopRunner is what runTurn drives: the agent loop, or the seam a test
// installs in its place (server.runLoop) so the whole socket path can be
// exercised without a brain.
func (s *Server) loopRunner() func(context.Context, string, string, string, <-chan agent.Steer, chan<- agent.RunEvent) error {
	if s.runLoop != nil {
		return s.runLoop
	}
	return s.loop.Run
}

// stanceFor returns the consent holder a message should update: the in-flight
// turn's holder when one exists (the message will become a steer), otherwise a
// fresh holder for the turn about to start.
func (s *Server) stanceFor(sessionID string) *turnctx.StanceHolder {
	s.turnsMu.Lock()
	defer s.turnsMu.Unlock()
	if st, ok := s.turns[sessionID]; ok && st != nil && st.stance != nil {
		return st.stance
	}
	return turnctx.NewStanceHolder()
}
