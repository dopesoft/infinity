package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// sessionMessageDTO is the on-the-wire shape returned by
// GET /api/sessions/{id}/messages. We reconstruct the visible conversation
// from mem_observations (the canonical capture log) so a browser refresh
// never loses what the user can see — even across core restarts.
type sessionMessageDTO struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// handleSessionMessages serves /api/sessions/{id}/messages by reading the
// UserPromptSubmit and TaskCompleted hooks for that session_id, in order.
//
// Tool calls and intermediate state are intentionally omitted; the goal is
// the user-visible chat transcript. Memory citations and tool invocations
// remain available via the Memory tab.
func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "messages" {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusOK, []sessionMessageDTO{})
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT hook_name, COALESCE(raw_text, ''), created_at
		FROM mem_observations
		WHERE session_id = $1
		  AND hook_name IN ('UserPromptSubmit', 'TaskCompleted')
		ORDER BY created_at ASC
		LIMIT 500
	`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	out := []sessionMessageDTO{}
	for rows.Next() {
		var hook, text string
		var createdAt time.Time
		if err := rows.Scan(&hook, &text, &createdAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		role := "assistant"
		if hook == "UserPromptSubmit" {
			role = "user"
		}
		out = append(out, sessionMessageDTO{Role: role, Text: text, CreatedAt: createdAt.UTC().Format(time.RFC3339)})
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, out)
}

// hydrateLoopSession lazily loads prior user/assistant turns into the
// agent's in-memory Session from mem_observations. Called from runTurn
// when the in-memory session is empty so post-refresh follow-ups still
// have conversation context (not just memory retrievals).
func (s *Server) hydrateLoopSession(r *http.Request, sessionID string) {
	if s.loop == nil || s.pool == nil || sessionID == "" {
		return
	}
	sess := s.loop.GetOrCreateSession(sessionID)
	if sess == nil {
		return
	}
	if len(sess.Snapshot()) > 0 {
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT hook_name, COALESCE(raw_text, ''), created_at
		FROM mem_observations
		WHERE session_id = $1
		  AND hook_name IN ('UserPromptSubmit', 'TaskCompleted')
		ORDER BY created_at ASC
		LIMIT 50
	`, sessionID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var hook, text string
		var createdAt time.Time
		if err := rows.Scan(&hook, &text, &createdAt); err != nil {
			return
		}
		_ = createdAt
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		role := llm.RoleAssistant
		if hook == "UserPromptSubmit" {
			role = llm.RoleUser
		}
		sess.Append(llm.Message{Role: role, Content: text})
	}
}
