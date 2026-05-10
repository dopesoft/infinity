package server

import (
	"encoding/json"
	"net/http"

	"github.com/dopesoft/infinity/core/internal/tools"
)

type statusResponse struct {
	Version  string   `json:"version"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Tools    []string `json:"tools"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := statusResponse{Version: s.cfg.Version, Tools: []string{}}
	if s.loop != nil {
		if p := s.loop.Provider(); p != nil {
			resp.Provider = p.Name()
			resp.Model = p.Model()
		}
		resp.Tools = s.loop.Tools().Names()
	}
	writeJSON(w, http.StatusOK, resp)
}

type toolDTO struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	out := []toolDTO{}
	if s.loop != nil {
		for _, name := range s.loop.Tools().Names() {
			if t, ok := s.loop.Tools().Get(name); ok {
				out = append(out, toolDTO{
					Name:        t.Name(),
					Description: t.Description(),
					Schema:      t.Schema(),
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMCP(w http.ResponseWriter, _ *http.Request) {
	out := []tools.MCPStatus{}
	if s.mcp != nil {
		out = s.mcp.Statuses()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	type sessionDTO struct {
		ID           string `json:"id"`
		StartedAt    string `json:"started_at"`
		MessageCount int    `json:"message_count"`
	}
	out := []sessionDTO{}
	if s.loop != nil {
		for _, sess := range s.loop.Sessions() {
			snap := sess.Snapshot()
			out = append(out, sessionDTO{
				ID:           sess.ID,
				StartedAt:    sess.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
				MessageCount: len(snap),
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
