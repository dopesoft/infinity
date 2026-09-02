package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/sessions"
)

// The definition of "this session has something to show" lives in
// internal/sessions (RenderableHooksSQL / HasRenderableSQL) so the transcript
// renderer here, the sessions LIST in api.go, and the auto-namer's sweep all
// read the SAME predicate. Aliased here to keep the query text below readable.
const (
	renderableHooksSQL = sessions.RenderableHooksSQL
	// What the MODEL is replayed when a session is faulted back in. Same
	// messages the transcript shows, minus the tool cards.
	conversationHooksSQL    = sessions.ConversationHooksSQL
	hydrationHooksSQL       = sessions.HydrationHooksSQL
	sessionHasRenderableSQL = sessions.HasRenderableSQL
)

type sessionAttachmentDTO struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	MimeType      string `json:"mime_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Text          string `json:"text,omitempty"`
	PreviewURL    string `json:"preview_url,omitempty"`
	StoragePath   string `json:"storage_path,omitempty"`
	ExtractStatus string `json:"extract_status,omitempty"`
	PageCount     int    `json:"page_count,omitempty"`
}

// sessionMessageDTO is the on-the-wire shape returned by
// GET /api/sessions/{id}/messages. We reconstruct the visible conversation
// from mem_observations (the canonical capture log) so a browser refresh
// never loses what the user can see - even across core restarts.
type sessionMessageDTO struct {
	Role        string                 `json:"role"`
	Text        string                 `json:"text"`
	CreatedAt   string                 `json:"created_at"`
	Attachments []sessionAttachmentDTO `json:"attachments,omitempty"`
	// Steered marks a user message that was injected mid-turn rather than
	// opening a fresh top-of-turn prompt. Studio uses this to rebuild the
	// "steered mid-turn" affordance after navigation/reload.
	Steered bool `json:"steered,omitempty"`
	// Kind discriminates non-plain messages so Studio can render them
	// with distinct chrome. Empty for ordinary user/assistant turns;
	// "dashboard_seed" for the context block injected by Discuss-with-Jarvis.
	Kind string `json:"kind,omitempty"`
	// SeedKind is the dashboard item kind (e.g. "activity", "memory") for
	// a "dashboard_seed" message - used as the card's header label.
	SeedKind string `json:"seed_kind,omitempty"`
	// CuriosityID links a "dashboard_seed" message back to an open
	// curiosity question (best-effort, by artifact-title match). When set,
	// the card renders an "Approve & fix" action.
	CuriosityID string `json:"curiosity_id,omitempty"`
	// Tool-call reconstruction (role="tool"): rebuilt from the captured
	// PostToolUse observation so the inline ToolCallCard survives navigation
	// and reload instead of vanishing. ToolInput is the raw arguments JSON.
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	ToolName    string          `json:"tool_name,omitempty"`
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput  string          `json:"tool_output,omitempty"`
	ToolIsError bool            `json:"tool_is_error,omitempty"`
}

func attachmentsFromPayload(payload string) []sessionAttachmentDTO {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var p struct {
		Attachments []sessionAttachmentDTO `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil || len(p.Attachments) == 0 {
		return nil
	}
	out := make([]sessionAttachmentDTO, 0, len(p.Attachments))
	for _, att := range p.Attachments {
		if strings.TrimSpace(att.Name) == "" && strings.TrimSpace(att.Text) == "" && strings.TrimSpace(att.PreviewURL) == "" && strings.TrimSpace(att.ID) == "" {
			continue
		}
		out = append(out, att)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	case "delete":
		s.handleSessionDelete(w, r, id)
		return
	default:
		http.NotFound(w, r)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusOK, []sessionMessageDTO{})
		return
	}

	// We reconstruct the transcript from two durable sources, UNIONed so a
	// single ORDER BY interleaves them by time:
	//   1. mem_observations — the user/assistant turns + tool cards.
	//   2. mem_turns rows with status='errored' — surfaced as synthetic
	//      'TaskErrored' rows so a provider/API error (OpenAI, rate limit,
	//      etc.) the boss saw live still shows on reload/another device.
	//      Without this the error only lived in transient WS state and
	//      vanished on refresh, so he couldn't ask Jarvis to fix it later.
	rows, err := s.pool.Query(r.Context(), `
		SELECT hook_name, raw_text, payload, created_at FROM (
			SELECT hook_name,
			       COALESCE(raw_text, '')     AS raw_text,
			       COALESCE(payload::text, '') AS payload,
			       created_at
			FROM mem_observations
			WHERE session_id = $1
			  AND hook_name IN (`+renderableHooksSQL+`)
			UNION ALL
			SELECT 'TaskErrored'                       AS hook_name,
			       COALESCE(error, '')                 AS raw_text,
			       ''                                  AS payload,
			       COALESCE(ended_at, started_at)      AS created_at
			FROM mem_turns
			WHERE session_id = $1::uuid
			  AND status = 'errored'
			  AND COALESCE(error, '') <> ''
		) combined
		WHERE EXISTS (
		    SELECT 1 FROM mem_sessions WHERE id = $1::uuid AND deleted_at IS NULL
		)
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
		var hook, text, payload string
		var createdAt time.Time
		if err := rows.Scan(&hook, &text, &payload, &createdAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		// Tool calls: rebuild the inline ToolCallCard from the captured
		// PostToolUse observation so it survives navigation/reload instead of
		// only living in transient WS state. The payload carries the tool name,
		// arguments, output, and the tool_call_id the live card used.
		if hook == "PostToolUse" || hook == "PostToolUseFailure" {
			var p struct {
				Name       string          `json:"name"`
				Input      json.RawMessage `json:"input"`
				Output     string          `json:"output"`
				ToolCallID string          `json:"tool_call_id"`
				// IsError rides the payload for steps captured OUTSIDE the
				// agent loop — a nested Claude Code job's own tool calls,
				// forwarded into the chat. Those must be able to render red
				// without being filed as PostToolUseFailure, which carries a
				// higher importance and feeds the self-improve backlog: a
				// grep that found nothing inside a build is not a failure of
				// Infinity's, and must not read as one.
				IsError bool `json:"is_error"`
			}
			_ = json.Unmarshal([]byte(payload), &p)
			if strings.TrimSpace(p.ToolCallID) == "" {
				continue
			}
			out = append(out, sessionMessageDTO{
				Role:        "tool",
				CreatedAt:   createdAt.UTC().Format(time.RFC3339),
				ToolCallID:  p.ToolCallID,
				ToolName:    p.Name,
				ToolInput:   p.Input,
				ToolOutput:  p.Output,
				ToolIsError: hook == "PostToolUseFailure" || p.IsError,
			})
			continue
		}

		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		// TaskErrored is the durable turn-level error (provider/API failure).
		// Studio renders it as the red error card via Kind="error".
		if hook == "TaskErrored" {
			out = append(out, sessionMessageDTO{
				Role:      "assistant",
				Kind:      "error",
				Text:      text,
				CreatedAt: createdAt.UTC().Format(time.RFC3339),
			})
			continue
		}
		// DashboardSeed is the Discuss-with-Jarvis context block. It reads
		// as a user-role turn to the model, but Studio renders it as a
		// distinct "from dashboard" card - so it carries a Kind + the
		// originating dashboard item kind parsed out of the seed payload.
		msg := sessionMessageDTO{
			Role:        "assistant",
			Text:        text,
			CreatedAt:   createdAt.UTC().Format(time.RFC3339),
			Attachments: attachmentsFromPayload(payload),
		}
		switch hook {
		case "UserPromptSubmit":
			msg.Role = "user"
			// Extract the steered flag so Studio can render the "↳ steered"
			// affordance on reload. The payload is {"steered":true} when the
			// message arrived via the steer channel; absent for normal turns.
			var p struct {
				Steered bool `json:"steered"`
			}
			_ = json.Unmarshal([]byte(payload), &p)
			msg.Steered = p.Steered
		case "DashboardSeed":
			msg.Role = "user"
			msg.Kind = "dashboard_seed"
			msg.SeedKind = seedKindFromPayload(payload)
			msg.CuriosityID = s.curiosityIDForSeed(r.Context(), payload)
		}
		out = append(out, msg)
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

	// The MOST RECENT 50, then put back in order.
	//
	// This took the OLDEST 50 and called it the conversation, so a long thread
	// picked back up after a Core restart came back as its opening and none of
	// its recent state - the boss saying "pick up where we left off" and being
	// answered out of the beginning of the thread. Where he left off is the
	// END of it.
	rows, err := s.pool.Query(r.Context(), `
		SELECT hook_name, raw_text, payload, created_at FROM (
			SELECT hook_name, COALESCE(raw_text, '') AS raw_text,
			       COALESCE(payload::text, '') AS payload, created_at
			FROM mem_observations
			WHERE session_id = $1
			  AND hook_name IN (`+hydrationHooksSQL+`)
			  AND EXISTS (
			    SELECT 1 FROM mem_sessions WHERE id = $1::uuid AND deleted_at IS NULL
			  )
			ORDER BY created_at DESC
			LIMIT 50
		) recent
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var hook, text, payload string
		var createdAt time.Time
		if err := rows.Scan(&hook, &text, &payload, &createdAt); err != nil {
			return
		}
		_ = createdAt
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		// DashboardSeed is injected context, but to the model it's the
		// opening user turn - so it hydrates as a user-role message.
		role := llm.RoleAssistant
		var atts []llm.Attachment
		if hook == "UserPromptSubmit" || hook == "DashboardSeed" || hook == sessions.AgentSelfPromptHook {
			role = llm.RoleUser
			// Files attached to that turn are reloaded from the store so the
			// brain sees them again after a Core restart, not just the chip.
			if ids := attachmentIDsFromPayload(payload); len(ids) > 0 && s.attachments != nil {
				atts = s.attachments.ToLLMMany(r.Context(), ids)
			}
		}
		sess.Append(llm.Message{Role: role, Content: text, Attachments: atts})
	}
}

// attachmentIDsFromPayload pulls the upload ids a UserPromptSubmit hook
// payload recorded (see llm.AttachmentsMeta).
func attachmentIDsFromPayload(payload string) []string {
	var ids []string
	for _, att := range attachmentsFromPayload(payload) {
		if id := strings.TrimSpace(att.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// seedKindFromPayload pulls the dashboard item kind ("activity", "memory",
// …) out of a DashboardSeed observation's payload JSON ({kind, id, snapshot}).
// Returns "" when the payload is missing or unparseable - the card just
// falls back to a generic header in that case.
func seedKindFromPayload(payload string) string {
	if strings.TrimSpace(payload) == "" {
		return ""
	}
	var p struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return ""
	}
	return p.Kind
}

// curiosityIDForSeed best-effort links a DashboardSeed observation back to
// an open curiosity question by matching the seeded artifact's title
// against mem_curiosity_questions.question - the same title-match the
// heartbeat-findings endpoint uses. Returns "" when there's no snapshot
// title or no open question matches; a miss just means the chat card
// shows no "Approve & fix" action, never an error.
func (s *Server) curiosityIDForSeed(ctx context.Context, payload string) string {
	if s.pool == nil || strings.TrimSpace(payload) == "" {
		return ""
	}
	var p struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil || len(p.Snapshot) == 0 {
		return ""
	}
	var art struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(p.Snapshot, &art); err != nil {
		return ""
	}
	title := strings.TrimSpace(art.Title)
	if title == "" {
		return ""
	}
	var id string
	if err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_curiosity_questions
		 WHERE question = $1 AND status = 'open'
		 ORDER BY created_at DESC
		 LIMIT 1
	`, title).Scan(&id); err != nil {
		return ""
	}
	return id
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

// handleSessionDelete serves POST /api/sessions/{id}/delete. Soft delete:
// stamps deleted_at, hides the session from the list / messages / hydrate
// paths, but never removes mem_observations (memories built from those
// observations stay grounded in their source). Any in-flight turn for the
// session is cancelled and the in-memory loop session is evicted so a
// subsequent WS frame can't accidentally write back into a tombstoned row.
//
// Idempotent: a re-delete of an already-deleted (or non-existent) session
// returns 200 with `deleted: false` and changes nothing.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db pool not configured"})
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE mem_sessions
		   SET deleted_at = NOW(),
		       ended_at   = COALESCE(ended_at, NOW())
		 WHERE id = $1::uuid
		   AND deleted_at IS NULL
	`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	deleted := tag.RowsAffected() > 0

	if deleted {
		// Drop any in-flight turn for this session so it can't keep
		// streaming into a tombstoned row, and evict the in-memory
		// session so a follow-up WS frame doesn't resurrect it.
		s.interruptTurn(id)
		if s.loop != nil {
			s.loop.ClearSession(id)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": deleted})
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
