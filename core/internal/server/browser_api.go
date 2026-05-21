package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// EmitBrowserFrame routes one live screencast frame to the given chat
// session's Studio tab. It rides the per-session broadcaster (sessionSender),
// so the frame reaches whichever WS is currently bound to the session and is
// dropped silently when none is (tab backgrounded, reconnecting) — the agent
// loop never blocks on the live view.
//
// Wired from serve.go as the browser registry's FrameSink. Kept as a method
// taking primitives so the server package doesn't depend on the browser
// package (no import cycle, clean layering).
func (s *Server) EmitBrowserFrame(chatSessionID string, seq int, frame, url, browserSessionID string) {
	if s == nil || chatSessionID == "" || frame == "" {
		return
	}
	s.sessionSender(chatSessionID)(wsServerEvent{
		Type:      "browser_frame",
		SessionID: chatSessionID,
		BrowserFrame: &wsBrowserFrame{
			Seq:              seq,
			Frame:            frame,
			URL:              url,
			BrowserSessionID: browserSessionID,
		},
	})
}

// handleBrowserClose tears down a live browser session (Studio "Stop"
// button). POST /api/browser/session/{id}/close.
func (s *Server) handleBrowserClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.BrowserClose == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser not configured"})
		return
	}
	// Path: /api/browser/session/{id}/close
	rest := strings.TrimPrefix(r.URL.Path, "/api/browser/session/")
	id := strings.TrimSuffix(rest, "/close")
	id = strings.Trim(id, "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.cfg.BrowserClose(ctx, id); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "closed": id})
}
