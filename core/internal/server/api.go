package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
		// Kind is who opened the session: 'user' for the boss's own chats,
		// anything else for machinery (cron / workflow / heartbeat / …). It
		// drives the Mine vs Automated switcher in the sessions list.
		Kind string `json:"kind,omitempty"`
		// Origin names the specific producer ("Inbox triage"), rendered as the
		// row's badge so an automated session says what it was FOR.
		Origin string `json:"origin,omitempty"`
		// Title is what to show. Falls back, in order, to the producer's name
		// and then the opening line of the conversation, so a row is never a
		// hex slug the boss can't place.
		Title string `json:"title,omitempty"`
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

	// ?kind=user (default) | automated | all — the sessions list switcher.
	// Unknown values are rejected rather than silently treated as the default:
	// a typo'd filter returning the wrong set is worse than an error.
	kindParam := r.URL.Query().Get("kind")
	kindSQL, kindOK := sessionKindFilter(kindParam)
	if !kindOK {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "kind must be one of: user, automated, all",
		})
		return
	}
	automatedOnly := strings.TrimSpace(strings.ToLower(kindParam)) != "" &&
		strings.TrimSpace(strings.ToLower(kindParam)) != "user" &&
		strings.TrimSpace(strings.ToLower(kindParam)) != "mine"

	out := []sessionDTO{}

	// Postgres is the source of truth - without this the page goes empty
	// every time core restarts (Railway redeploys, OOM kills, etc.),
	// even though mem_observations still has all the messages. Sessions
	// recover when the user reopens them in Studio (`hydrateLoopSession`
	// repopulates the in-memory map), but the list view should never need
	// that round-trip.
	if s.pool != nil {
		// Two independent reasons a session row is not history the boss wants:
		//
		//   kind <> 'user'  — background machinery (cron, heartbeat, voyager,
		//     the verification harness, the phone backfill's FK containers).
		//     It runs in sessions on purpose; those just aren't his.
		//
		//   nothing to render — the boss's rule, verbatim: "i dont care if we
		//     secretly use sessions behind the scenes but an empty session is of
		//     no use to me." So the list offers a session only when the
		//     transcript endpoint would actually put something on screen. This
		//     is deliberately a property of the DATA, not of the writer's good
		//     intentions: any present or future caller that opens a session and
		//     writes nothing readable is invisible here for free, whatever kind
		//     it stamps and whether or not it remembered to stamp one. That's
		//     what keeps the blank rows from coming back a third time.
		//
		// A brand-new live session legitimately has nothing yet — it's excluded
		// here and re-added from the `live` map below, so it still shows.
		//
		// msg_count uses the same predicate rather than COUNT(*) over every
		// observation: the raw count is what advertised the backfill's blank
		// sessions as "1 message" and then showed an empty screen.
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
			       COALESCE((SELECT COUNT(*) FROM mem_observations o
			                  WHERE o.session_id = s.id
			                    AND o.hook_name IN (`+renderableHooksSQL+`)), 0) AS msg_count,
			       COALESCE(s.kind, 'user') AS kind,
			       COALESCE(s.origin_ref, '{}'::jsonb)::text AS origin_ref,
			       COALESCE(s.seeded_from, '{}'::jsonb)::text AS seeded_from,
			       -- The opening line, for a session whose auto-title hasn't
			       -- been drafted yet. Cheaper than it looks: indexed on
			       -- session_id, one row, and only for the 50 listed.
			       COALESCE((SELECT o.raw_text FROM mem_observations o
			                  WHERE o.session_id = s.id
			                    AND o.hook_name IN ('UserPromptSubmit', 'DashboardSeed')
			                    AND btrim(COALESCE(o.raw_text,'')) <> ''
			                  ORDER BY o.created_at LIMIT 1), '') AS first_message
			  FROM mem_sessions s
			 WHERE s.deleted_at IS NULL
			   AND `+kindSQL+`
			   AND `+sessionHasRenderableSQL+`
			   AND `+sessionJobIsListableSQL+`
			 ORDER BY COALESCE(s.last_run_at, s.started_at) DESC
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
				var originRaw, seededRaw, firstMessage string
				if err := rows.Scan(&d.ID, &d.Name, &started, &ended,
					&d.Project, &d.ProjectPath, &d.ProjectTemplate,
					&d.DevPort, &lastRun, &d.MessageCount,
					&d.Kind, &originRaw, &seededRaw, &firstMessage); err != nil {
					log.Printf("handleSessions scan: %v", err)
					continue
				}
				var origin map[string]any
				if originRaw != "" {
					_ = json.Unmarshal([]byte(originRaw), &origin)
				}
				d.Origin = sessionOriginLabel(d.Kind, origin)
				if d.Origin == "" && seededRaw != "" {
					var seed map[string]any
					_ = json.Unmarshal([]byte(seededRaw), &seed)
					d.Origin = seedOriginLabel(seed)
				}
				// The drafted name always wins: it describes what actually
				// happened in the session. The derived title only stands in
				// while there isn't one.
				d.Title = d.Name
				if strings.TrimSpace(d.Title) == "" {
					d.Title = sessionFallbackTitle(d.Kind, origin, firstMessage)
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
	// Skipped on the Automated tab: a session live in RAM without a row yet
	// has no kind to test, so adding it there would put a chat the boss just
	// started under "Automated".
	if s.loop != nil && !automatedOnly {
		for _, sess := range s.loop.Sessions() {
			if _, ok := live[sess.ID]; !ok {
				continue
			}
			out = append(out, sessionDTO{
				ID:           sess.ID,
				StartedAt:    sess.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
				MessageCount: len(sess.Snapshot()),
				Live:         true,
				Kind:         "user",
			})
		}
	}

	// The DB rows arrive already newest-first (ORDER BY activity DESC), but
	// the RAM-only live sessions above are appended in Go map-iteration
	// order, which is non-deterministic. That left a brand-new session
	// (live in RAM before its mem_sessions row commits) stranded at the end
	// of the slice, so Studio rendered it at the BOTTOM of the "Today" group
	// instead of the top. Re-sort the merged slice so the response is one
	// consistent order regardless of each row's source. Latest ACTIVITY
	// first (last turn, falling back to creation) - a session continued
	// today must surface at the top even if it was created weeks ago, or
	// its fresh auto-title stays buried (the "voice chats don't get a
	// title" report). RFC3339 UTC strings compare correctly as strings.
	activity := func(d sessionDTO) string {
		if d.LastRunAt != "" {
			return d.LastRunAt
		}
		return d.StartedAt
	}
	sort.SliceStable(out, func(i, j int) bool {
		return activity(out[i]) > activity(out[j])
	})

	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
