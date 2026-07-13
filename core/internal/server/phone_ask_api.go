package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/google/uuid"
)

// POST /api/phone/ask - the dashboard header's "have Jarvis make a call"
// button. The boss types one natural-language errand ("call Sanson's Pizza
// in Frisco and order a pepperoni for pickup") and it runs as a detached
// background agent turn: the agent finds the number if needed, writes the
// brief, places the call via phone_call, and the outcome comes back as a
// push + the Phone card (the monitor owns that delivery).
//
// phone_call is PRE-APPROVED for this one session: the boss typing the
// errand IS the approval, so re-prompting him through the Trust queue for
// the very call he just commissioned would be pure friction (same pattern
// as send_reply pre-approval in surface_action_api.go). Anything else the
// turn attempts still gates normally.
func (s *Server) handlePhoneAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.loop == nil {
		writeError(w, http.StatusServiceUnavailable, "agent loop not available")
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// A REAL uuid session with a mem_sessions row (kind='user' - the boss
	// commissioned this from inside the app). A prefixed pseudo-id here
	// poisons every mem_* hook write with uuid-cast errors (SQLSTATE 22P02)
	// and the turn runs memory-blind - the known non-UUID-session bug class.
	sessionID := uuid.NewString()
	if s.pool != nil {
		_, _ = s.pool.Exec(r.Context(), `
			INSERT INTO mem_sessions (id, kind, origin_ref, started_at)
			VALUES ($1::uuid, 'user', '{"kind":"phone_ask"}'::jsonb, NOW())
			ON CONFLICT (id) DO NOTHING`, sessionID)
	}
	if s.trust != nil {
		s.trust.PreApproveTools(r.Context(), sessionID, []string{"phone_call"})
	}

	label := "Call errand: " + clipLabel(prompt, 60)
	turnPrompt := prompt + "\n\n" +
		"(The boss typed this from the dashboard's call button. If it needs a phone " +
		"call, follow your phone-calls skill: find the number first if you don't have " +
		"it, write a tight brief, and place the call with phone_call. The call's " +
		"outcome reaches him automatically when it ends - your job is the setup.)"

	go func() {
		_ = runs.Track(context.Background(), runs.KindPhoneAsk, sessionID, label, runs.SourceAgent, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			out := make(chan agent.RunEvent, 256)
			go func() {
				for ev := range out {
					sendRunEventToWS(func(frame wsServerEvent) {
						s.broadcastAll(frame)
					}, ev)
				}
			}()
			err := s.loop.Run(ctx, sessionID, turnPrompt, "", nil, out)
			close(out)
			if err != nil {
				s.broadcastAll(wsServerEvent{Type: "error", SessionID: sessionID, Message: err.Error()})
			}
			return err
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"kind":       string(runs.KindPhoneAsk),
		"session_id": sessionID,
	})
}

func clipLabel(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// GET /api/phone/contacts - the boss's phone book, and the same book the agent
// resolves "call Ariana" against (mem_contacts, migration 177).
//
// This used to read straight off the phone:history:* cells, which meant a
// contact could only exist AFTER a call and had no name index, so nothing could
// turn a name into a number. Same surface (PhoneCard -> ContactBookModal), same
// route, real spine: contacts are now written the moment one is learned, by a
// call, by the boss saying a number, or by a web search for a business.
//
// The per-number call narrative (phone:history:*) is still joined in, because
// "what we last talked about" is a different thing from "who this is", and the
// book reads better with both.
func (s *Server) handlePhoneContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT c.name, c.number, c.kind, c.location, c.note, c.times_called,
		       COALESCE(c.last_called_at, c.updated_at) AS at,
		       COALESCE(h.value #>> '{}', '') AS history
		FROM mem_contacts c
		LEFT JOIN mem_agent_state h
		       ON h.key = 'phone:history:' || right(regexp_replace(c.number, '[^0-9]', '', 'g'), 10)
		ORDER BY COALESCE(c.last_called_at, c.updated_at) DESC
		LIMIT 200
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load contacts")
		return
	}
	defer rows.Close()
	type contact struct {
		Number      string `json:"number"`
		Name        string `json:"name,omitempty"`
		Kind        string `json:"kind,omitempty"`
		Location    string `json:"location,omitempty"`
		Note        string `json:"note,omitempty"`
		TimesCalled int    `json:"times_called"`
		Last        string `json:"last"`
		History     string `json:"history"`
		UpdatedAt   string `json:"updated_at"`
	}
	out := []contact{}
	for rows.Next() {
		var c contact
		var at time.Time
		if rows.Scan(&c.Name, &c.Number, &c.Kind, &c.Location, &c.Note, &c.TimesCalled, &at, &c.History) != nil {
			continue
		}
		c.UpdatedAt = at.UTC().Format(time.RFC3339)
		// "Last" is the headline line the modal shows: the newest history entry
		// if we have one, otherwise whatever we know about them.
		c.Last = c.Note
		if c.History != "" {
			first := c.History
			if i := strings.Index(first, " | "); i > 0 {
				first = first[:i]
			}
			c.Last = first
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}
