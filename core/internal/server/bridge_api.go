package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// bridge_api.go - Studio's view into the bridge layer.
//
//   GET  /api/bridge/status               health of both bridges + cached snapshot
//   GET  /api/bridge/session/{id}         this session's preference + which bridge it'd hit right now
//   POST /api/bridge/session/{id}         { preference: "auto" | "mac" | "cloud" } - flip a session
//   POST /api/bridge/refresh              force the router to re-probe (cache bust)
//
// Studio polls /status every 10s to keep the persistent pill in sync.
// The session endpoints power the per-session preference drawer.

type bridgeSessionResponse struct {
	SessionID   string `json:"session_id"`
	Preference  string `json:"preference"`
	ActiveKind  string `json:"active_kind,omitempty"`
	ActiveURL   string `json:"active_url,omitempty"`
	WhyActive   string `json:"why_active,omitempty"`
	BridgeError string `json:"bridge_error,omitempty"`
}

func (s *Server) handleBridgeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.bridgeRouter == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    false,
			"mac_healthy":   false,
			"cloud_healthy": false,
		})
		return
	}
	st := s.bridgeRouter.Refresh(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    true,
		"mac_healthy":   st.MacHealthy,
		"cloud_healthy": st.CloudHealthy,
		"mac_url":       st.MacURL,
		"cloud_url":     st.CloudURL,
		"checked_at":    st.CheckedAt,
	})
}

func (s *Server) handleBridgeRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.bridgeRouter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge router not configured"})
		return
	}
	s.bridgeRouter.Invalidate()
	st := s.bridgeRouter.Refresh(r.Context())
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBridgeSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/bridge/session/")
	id := strings.SplitN(rest, "/", 2)[0]
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.bridgeSessionGet(w, r, id)
	case http.MethodPost:
		s.bridgeSessionSet(w, r, id)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

func (s *Server) bridgeSessionGet(w http.ResponseWriter, r *http.Request, id string) {
	resp := bridgeSessionResponse{
		SessionID:  id,
		Preference: string(bridge.PrefAuto),
	}
	if s.bridgePrefs != nil {
		resp.Preference = string(s.bridgePrefs(r.Context(), id))
	}
	if s.bridgeRouter != nil {
		b, why, err := s.bridgeRouter.For(r.Context(), bridge.Preference(resp.Preference))
		resp.WhyActive = why
		if err != nil {
			resp.BridgeError = err.Error()
		} else if b != nil {
			resp.ActiveKind = string(b.Name())
			resp.ActiveURL = b.BaseURL()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) bridgeSessionSet(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Preference string `json:"preference"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	switch bridge.Preference(body.Preference) {
	case bridge.PrefAuto, bridge.PrefMac, bridge.PrefCloud:
		// valid
	default:
		http.Error(w, "preference must be auto | mac | cloud", http.StatusBadRequest)
		return
	}
	if s.pool == nil {
		http.Error(w, "db not configured", http.StatusServiceUnavailable)
		return
	}
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE mem_sessions SET bridge_preference = $1 WHERE id::text = $2`,
		body.Preference, id,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Force a re-probe so the next request reflects the new pin.
	if s.bridgeRouter != nil {
		s.bridgeRouter.Invalidate()
	}
	s.bridgeSessionGet(w, r, id)
}
