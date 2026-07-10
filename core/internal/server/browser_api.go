package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/browser"
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

// handleBrowserSession dispatches the live-browser control verbs on
// POST /api/browser/session/{id}/{verb}: close (Stop button), navigate
// (editable URL bar), and input (the boss's manual click/type/scroll takeover
// of the screencast). All three act on a live session the registry already
// tracks; the registry is the bridge-aware authority so these stay engine-
// agnostic.
func (s *Server) handleBrowserSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Path: /api/browser/session/{id}/{verb}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/browser/session/"), "/")
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id and verb required"})
		return
	}
	id, verb := rest[:slash], rest[slash+1:]
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}
	switch verb {
	case "close":
		s.browserClose(w, r, id)
	case "navigate":
		s.browserNavigate(w, r, id)
	case "input":
		s.browserInput(w, r, id)
	case "control":
		s.browserControl(w, r, id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown verb: " + verb})
	}
}

// browserControl flips the session's driver. Body: {"controller":"agent"|"human"}.
// "agent" is the Hand-back button after a takeover; "human" is the explicit
// take-over affordance (manual input also claims control implicitly, so this
// is mostly the hand-back path).
func (s *Server) browserControl(w http.ResponseWriter, r *http.Request, id string) {
	if s.cfg.BrowserControl == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser not configured"})
		return
	}
	var body struct {
		Controller string `json:"controller"`
		Reason     string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		if body.Controller == "agent" {
			reason = "handed back"
		} else {
			reason = "took over"
		}
	}
	if err := s.cfg.BrowserControl(id, body.Controller, reason); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "controller": body.Controller})
}

// EmitPhoneLive broadcasts one live-call update (a transcript line, or the
// final done+summary) to every connected tab. Calls aren't bound to a chat
// session — the Phone card lives on the dashboard — so this rides
// broadcastAll rather than the per-session sender.
func (s *Server) EmitPhoneLive(callID, direction, number, speaker, text string, done bool, summary, status string) {
	if s == nil || callID == "" {
		return
	}
	s.broadcastAll(wsServerEvent{
		Type: "phone_live",
		PhoneLive: &wsPhoneLive{
			CallID:    callID,
			Direction: direction,
			Number:    number,
			Speaker:   speaker,
			Text:      text,
			Done:      done,
			Summary:   summary,
			Status:    status,
		},
	})
}

// EmitBrowserControl broadcasts a takeover state change to the session's
// Studio tab so the Preview pane can render "you're driving / hand back"
// the moment control flips - whether the flip came from the agent
// (browser_request_takeover), the boss's first manual input, or the
// explicit control endpoint above.
func (s *Server) EmitBrowserControl(chatSessionID, browserSessionID, controller, reason string) {
	if s == nil || chatSessionID == "" {
		return
	}
	s.sessionSender(chatSessionID)(wsServerEvent{
		Type:      "browser_control",
		SessionID: chatSessionID,
		BrowserControl: &wsBrowserControl{
			BrowserSessionID: browserSessionID,
			Controller:       controller,
			Reason:           reason,
		},
	})
}

func (s *Server) browserClose(w http.ResponseWriter, r *http.Request, id string) {
	if s.cfg.BrowserClose == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser not configured"})
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

func (s *Server) browserNavigate(w http.ResponseWriter, r *http.Request, id string) {
	if s.cfg.BrowserNavigate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser not configured"})
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.cfg.BrowserNavigate(ctx, id, body.URL); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) browserInput(w http.ResponseWriter, r *http.Request, id string) {
	if s.cfg.BrowserInput == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "browser not configured"})
		return
	}
	var ev browser.InputEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// Snappy timeout: human input is high-frequency and must feel instant.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.cfg.BrowserInput(ctx, id, ev); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
