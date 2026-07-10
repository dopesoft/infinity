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

	sessionID := "phone-ask-" + uuid.NewString()
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

// GET /api/phone/contacts - the dial-back book: every number Jarvis has
// call history with, named when the history is (the phone_call `name` is
// stamped as a "Name: ..." prefix on the newest entry). Read straight off
// the phone:history:* keyed-state cells - no separate contacts table.
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
		SELECT key, value #>> '{}', updated_at FROM mem_agent_state
		WHERE key LIKE 'phone:history:%' ORDER BY updated_at DESC LIMIT 50
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load contacts")
		return
	}
	defer rows.Close()
	type contact struct {
		Number    string `json:"number"`
		Name      string `json:"name,omitempty"`
		Last      string `json:"last"`
		History   string `json:"history"`
		UpdatedAt string `json:"updated_at"`
	}
	out := []contact{}
	for rows.Next() {
		var key, hist string
		var at time.Time
		if rows.Scan(&key, &hist, &at) != nil {
			continue
		}
		c := contact{Number: "+" + strings.TrimPrefix(key, "phone:history:"), UpdatedAt: at.UTC().Format(time.RFC3339)}
		// Newest entry leads; a named call reads "Goodfellas Pizza: Outbound call Jul 10 2026...".
		first := hist
		if i := strings.Index(hist, " | "); i > 0 {
			first = hist[:i]
		}
		if i := strings.Index(first, ": "); i > 0 && !strings.HasPrefix(first, "Inbound") && !strings.HasPrefix(first, "Outbound") {
			c.Name = first[:i]
		}
		c.Last = first
		c.History = hist
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}
