package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
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

type wsClientMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
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
	// Steered marks a delta/complete that resulted from a mid-turn steer
	// (used by the studio transcript to render the "↳ steered" badge on
	// reconstructed bubbles). Empty by default.
	Steered bool `json:"steered,omitempty"`
	// Intent carries the per-turn IntentFlow classification. Only present
	// on type="intent" frames. Studio's IntentStream panel reads this
	// directly; the chat transcript ignores it.
	Intent *wsIntent `json:"intent,omitempty"`
	// FindingKind is set on type="proactive_message" frames so Studio can
	// render an icon + tone consistent with the Heartbeat tab - e.g.
	// "surprise" gets a lightbulb, "security" gets a shield.
	FindingKind string `json:"finding_kind,omitempty"`
	// CuriosityID is set on type="proactive_message" frames for findings
	// backed by a mem_curiosity_questions row, so the chat card can offer
	// an "Approve & fix" action that round-trips to the decide endpoint.
	CuriosityID string `json:"curiosity_id,omitempty"`
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

	// connCtx is the lifetime of this WebSocket. Every turn started from
	// this connection inherits it so a disconnect (browser close, network
	// flap, reconnect) tears down all in-flight turns from this socket.
	// The turn loop itself observes ctx.Done() and emits a clean
	// `complete{stop_reason: "interrupted"}` rather than a bare error.
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
			if s.steerTurn(msg.SessionID, msg.Content, send) {
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
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, msg.Content, send)
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
			// Auto-route to steer when a turn is already running for
			// this session. This lets the studio compose+send while
			// streaming without having to switch message types - the
			// server figures it out.
			if s.steerTurn(sessionID, msg.Content, send) {
				continue
			}
			// First message for this session since startup (or after
			// the agent restarted): preload prior turns from
			// mem_observations so the model sees the same conversation
			// the user does.
			s.hydrateLoopSession(r, sessionID)
			s.startTurn(connCtx, userID, sessionID, msg.Content, send)
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
			s.startTurn(connCtx, userID, sessionID, "", send)
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
// startTurn spawns the agent loop for one turn. The model is resolved
// server-side from the settings store (set by Studio's chip + Settings
// page) rather than carried on the WS frame - that way a single source
// of truth drives both the live chip and the Settings page, and a
// hostile client can't smuggle an arbitrary model id through the wire.
func (s *Server) startTurn(_ context.Context, userID, sessionID, content string, _ func(wsServerEvent)) {
	// Use a fresh background context so the WS dying doesn't cancel this
	// turn. The 5-minute timeout below is the only deadline that applies.
	base := context.Background()
	// Resolve effective model from the persisted setting; empty string
	// means the agent loop falls back to the provider's boot default.
	model := s.resolveModel(base)
	// Attach the auth identity so any tool calls / hook fires that key
	// off the request user have it available. Then wrap in a per-turn
	// timeout so a wedged provider doesn't pin a goroutine forever.
	ctxWithUser := context.WithValue(base, auth.ContextKey{}, userID)
	turnCtx, cancel := context.WithTimeout(ctxWithUser, 5*time.Minute)
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
		s.runTurn(turnCtx, sessionID, content, model, state.steer, send)
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

// runTurn drives one agent turn and pumps RunEvent → WS frames. The caller
// (startTurn) owns the cancel + steer channel via the turns registry; we
// receive the steer channel as a receive-only param so the agent loop can
// drain it between iterations. ctx is already wrapped with the per-turn
// 5-minute timeout, so we don't re-wrap it here.
func (s *Server) runTurn(ctx context.Context, sessionID, content, model string, steer <-chan string, send func(wsServerEvent)) {
	events := make(chan agent.RunEvent, 128)
	done := make(chan struct{})

	go func() {
		defer close(done)
		if err := s.loop.Run(ctx, sessionID, content, model, steer, events); err != nil {
			send(wsServerEvent{Type: "error", SessionID: sessionID, Message: err.Error()})
		}
		close(events)
	}()

	/* Accumulate the assistant's streamed text so on EventComplete we can
	 * write the full user/assistant pair into the WorkingBuffer when the
	 * context window is past threshold. We only need text deltas - tool
	 * calls aren't mirrored into the buffer (they'd churn it on every
	 * iteration without adding recoverable content). */
	var assistantText strings.Builder

	for ev := range events {
		switch ev.Kind {
		case agent.EventDelta:
			assistantText.WriteString(ev.TextDelta)
			send(wsServerEvent{Type: "delta", SessionID: ev.SessionID, Text: ev.TextDelta})
		case agent.EventThinking:
			send(wsServerEvent{Type: "thinking", SessionID: ev.SessionID, Text: ev.ThinkingDelta})
		case agent.EventToolCall:
			if ev.ToolCall != nil {
				// Forward the full ToolEvent including the gate's
				// awaiting_approval signal - without these fields the
				// browser never knows to render the inline Approve /
				// Deny buttons and the user watches the card spin
				// while the agent loop is blocked on WaitForDecision.
				send(wsServerEvent{
					Type:      "tool_call",
					SessionID: ev.SessionID,
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
		case agent.EventToolResult:
			if ev.ToolResult != nil {
				send(wsServerEvent{
					Type:      "tool_result",
					SessionID: ev.SessionID,
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
				usage["input"] = ev.Usage.Input
				usage["output"] = ev.Usage.Output
			}
			send(wsServerEvent{
				Type:       "complete",
				SessionID:  ev.SessionID,
				Usage:      usage,
				StopReason: ev.StopReason,
			})
			/* Mirror this exchange into mem_working_buffer iff the
			 * model's context window crossed the proactive threshold
			 * (default 0.6 of max). Heuristic ctx_max - provider
			 * interface doesn't expose context window, so we infer
			 * from the model id. Fail-open: any error here is silent
			 * because the turn already succeeded. */
			usedTokens := 0
			if ev.Usage != nil {
				usedTokens = ev.Usage.Input + ev.Usage.Output
			}
			s.captureWorkingBuffer(ctx, ev.SessionID, content, assistantText.String(), usedTokens)
		case agent.EventError:
			send(wsServerEvent{Type: "error", SessionID: ev.SessionID, Message: ev.Error})
		}
	}

	<-done
}
