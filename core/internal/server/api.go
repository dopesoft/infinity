package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

type statusResponse struct {
	Version  string   `json:"version"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Tools    []string `json:"tools"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{Version: s.cfg.Version, Tools: []string{}}
	if s.loop != nil {
		if p := s.loop.Provider(); p != nil {
			resp.Provider = p.Name()
			resp.Model = p.Model()
		}
		resp.Tools = s.loop.Tools().Names()
	}
	// Effective model - settings store override beats the provider's
	// boot default so /api/status reflects what the next turn will
	// actually run against (not the env var). Studio's status footer
	// and the Settings page both read this.
	if s.settings != nil {
		if override := s.settings.GetModel(r.Context()); override != "" {
			resp.Model = override
		}
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
	// Failed-to-connect servers leave Tools nil, which marshals as JSON
	// null. The studio crashes on `s.tools.length` if we let that
	// through - fix the wire format here so every entry has [].
	for i := range out {
		if out[i].Tools == nil {
			out[i].Tools = []string{}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	type sessionDTO struct {
		ID              string `json:"id"`
		Name            string `json:"name,omitempty"`
		StartedAt       string `json:"started_at"`
		EndedAt         string `json:"ended_at,omitempty"`
		Project         string `json:"project,omitempty"`
		ProjectPath     string `json:"project_path,omitempty"`
		ProjectTemplate string `json:"project_template,omitempty"`
		DevPort         int    `json:"dev_port,omitempty"`
		LastRunAt       string `json:"last_run_at,omitempty"`
		MessageCount    int    `json:"message_count"`
		Live            bool   `json:"live"`
	}

	// Build a set of session IDs that are alive in this core process's
	// memory right now so we can tag DB-backed rows as "live" in the UI.
	live := map[string]int{}
	if s.loop != nil {
		for _, sess := range s.loop.Sessions() {
			live[sess.ID] = len(sess.Snapshot())
		}
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	out := []sessionDTO{}

	// Postgres is the source of truth - without this the page goes empty
	// every time core restarts (Railway redeploys, OOM kills, etc.),
	// even though mem_observations still has all the messages. Sessions
	// recover when the user reopens them in Studio (`hydrateLoopSession`
	// repopulates the in-memory map), but the list view should never need
	// that round-trip.
	if s.pool != nil {
		rows, err := s.pool.Query(r.Context(), `
			SELECT s.id::text,
			       COALESCE(s.name, ''),
			       s.started_at,
			       s.ended_at,
			       COALESCE(s.project, ''),
			       COALESCE(s.project_path, ''),
			       COALESCE(s.project_template, ''),
			       COALESCE(s.dev_port, 0),
			       s.last_run_at,
			       COALESCE((SELECT COUNT(*) FROM mem_observations o WHERE o.session_id = s.id), 0) AS msg_count
			  FROM mem_sessions s
			 WHERE s.deleted_at IS NULL
			 ORDER BY s.started_at DESC
			 LIMIT $1
		`, limit)
		if err != nil {
			log.Printf("handleSessions: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var d sessionDTO
				var started time.Time
				var ended, lastRun *time.Time
				if err := rows.Scan(&d.ID, &d.Name, &started, &ended,
					&d.Project, &d.ProjectPath, &d.ProjectTemplate,
					&d.DevPort, &lastRun, &d.MessageCount); err != nil {
					log.Printf("handleSessions scan: %v", err)
					continue
				}
				d.StartedAt = started.UTC().Format(time.RFC3339)
				if ended != nil {
					d.EndedAt = ended.UTC().Format(time.RFC3339)
				}
				if lastRun != nil {
					d.LastRunAt = lastRun.UTC().Format(time.RFC3339)
				}
				if _, ok := live[d.ID]; ok {
					d.Live = true
				}
				out = append(out, d)
				delete(live, d.ID)
			}
		}
	}

	// Any session that's live in RAM but doesn't have a mem_sessions row
	// yet (extremely early in a brand-new session, race window) - surface
	// it anyway so the UI never lies about what's running.
	if s.loop != nil {
		for _, sess := range s.loop.Sessions() {
			if _, ok := live[sess.ID]; !ok {
				continue
			}
			out = append(out, sessionDTO{
				ID:           sess.ID,
				StartedAt:    sess.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
				MessageCount: len(sess.Snapshot()),
				Live:         true,
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
