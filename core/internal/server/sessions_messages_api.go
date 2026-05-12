package server

import (
	"encoding/json"
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
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}
	switch parts[1] {
	case "messages":
		// fall through to the existing implementation below.
	case "rename":
		s.handleSessionRename(w, r, id)
		return
	case "project":
		s.handleSessionProject(w, r, id)
		return
	default:
		http.NotFound(w, r)
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

// handleSessionRename serves POST /api/sessions/{id}/rename {"name": "..."}.
// Empty name clears the column so the Haiku auto-namer can fire again. The
// rename runs through the Namer so the inflight map blocks a concurrent
// auto-name race.
func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if s.namer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "namer not configured"})
		return
	}
	if err := s.namer.Rename(r.Context(), id, body.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "name": strings.TrimSpace(body.Name)})
}

// handleSessionProject serves POST /api/sessions/{id}/project. Body shape:
//
//	{"project_path": "/Users/.../my-app",
//	 "project_template": "nextjs",
//	 "dev_port": 3000}
//
// All fields optional. Setting an empty project_path clears the project
// attachment (rare; mostly used by tests). dev_port=0 leaves the column
// untouched so the supervisor can keep its detected value.
func (s *Server) handleSessionProject(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db pool not configured"})
		return
	}
	var body struct {
		ProjectPath     *string `json:"project_path"`
		ProjectTemplate *string `json:"project_template"`
		DevPort         *int    `json:"dev_port"`
		MarkRun         bool    `json:"mark_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Build a sparse UPDATE so an unset field doesn't clobber existing data.
	sets := []string{}
	args := []any{id}
	if body.ProjectPath != nil {
		args = append(args, strings.TrimSpace(*body.ProjectPath))
		sets = append(sets, "project_path = NULLIF($"+itoa(len(args))+", '')")
	}
	if body.ProjectTemplate != nil {
		args = append(args, strings.TrimSpace(*body.ProjectTemplate))
		sets = append(sets, "project_template = NULLIF($"+itoa(len(args))+", '')")
	}
	if body.DevPort != nil && *body.DevPort > 0 {
		args = append(args, *body.DevPort)
		sets = append(sets, "dev_port = $"+itoa(len(args)))
	}
	if body.MarkRun {
		sets = append(sets, "last_run_at = NOW()")
	}
	if len(sets) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no fields to update"})
		return
	}

	// Insert the row if it doesn't exist yet (Studio can post project
	// metadata before any agent turn has materialized the row).
	if _, err := s.pool.Exec(r.Context(),
		`INSERT INTO mem_sessions (id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	q := "UPDATE mem_sessions SET " + strings.Join(sets, ", ") + " WHERE id = $1::uuid"
	if _, err := s.pool.Exec(r.Context(), q, args...); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "ok"})
}

// itoa is a tiny strconv.Itoa shadow that keeps the imports of this file
// minimal (no need to pull strconv just for placeholders). Inlined here
// rather than imported because it's only used by handleSessionProject.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
