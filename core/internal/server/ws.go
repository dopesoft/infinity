package server

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

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
}

type wsToolEvent struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input,omitempty"`
	Output    string         `json:"output,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	EndedAt   time.Time      `json:"ended_at,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.loop == nil {
		http.Error(w, "agent loop not configured", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

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
		case "message":
			sessionID := msg.SessionID
			if sessionID == "" {
				sessionID = uuid.NewString()
			}
			// If this WS connection is the first time we're seeing this
			// session_id since startup (e.g. after a browser refresh or core
			// restart), preload prior turns from mem_observations so the
			// model sees the same conversation the user does.
			s.hydrateLoopSession(r, sessionID)
			s.runTurn(r.Context(), sessionID, msg.Content, send)
		default:
			send(wsServerEvent{Type: "error", SessionID: msg.SessionID, Message: "unknown type: " + msg.Type})
		}
	}
}

func (s *Server) runTurn(ctx context.Context, sessionID, content string, send func(wsServerEvent)) {
	turnCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	events := make(chan agent.RunEvent, 128)
	done := make(chan struct{})

	go func() {
		defer close(done)
		if err := s.loop.Run(turnCtx, sessionID, content, events); err != nil {
			send(wsServerEvent{Type: "error", SessionID: sessionID, Message: err.Error()})
		}
		close(events)
	}()

	for ev := range events {
		switch ev.Kind {
		case agent.EventDelta:
			send(wsServerEvent{Type: "delta", SessionID: ev.SessionID, Text: ev.TextDelta})
		case agent.EventToolCall:
			if ev.ToolCall != nil {
				send(wsServerEvent{
					Type:      "tool_call",
					SessionID: ev.SessionID,
					ToolCall: &wsToolEvent{
						ID:        ev.ToolCall.ID,
						Name:      ev.ToolCall.Name,
						Input:     ev.ToolCall.Input,
						StartedAt: ev.ToolCall.StartedAt,
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
		case agent.EventError:
			send(wsServerEvent{Type: "error", SessionID: ev.SessionID, Message: ev.Error})
		}
	}

	<-done
}
