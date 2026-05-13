// Dashboard tools — let Jarvis write to the dashboard surfaces from chat.
//
// Eight tools land here, organized by surface:
//
//	task_create / task_update / task_done            → mem_tasks
//	pursuit_create / pursuit_checkin                  → mem_pursuits + mem_pursuit_checkins
//	followup_snooze / followup_dismiss                → mem_followups
//	saved_add                                         → mem_saved
//
// Each tool is intentionally narrow — one mutation per name — so the
// model has clear targets and we can grant/revoke per-tool risk later
// (none of these route through ClaudeCodeGate; they're internal data
// edits, not shell commands).
//
// Register via RegisterDashboardTools from serve.go after the pool is
// wired. No-op if pool is nil so chat-only deployments don't break.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterDashboardTools wires every dashboard mutation as a native tool.
func RegisterDashboardTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&taskCreate{pool: pool})
	r.Register(&taskUpdate{pool: pool})
	r.Register(&taskDone{pool: pool})
	r.Register(&pursuitCreate{pool: pool})
	r.Register(&pursuitCheckin{pool: pool})
	r.Register(&followupSnooze{pool: pool})
	r.Register(&followupDismiss{pool: pool})
	r.Register(&savedAdd{pool: pool})
}

// ── task_create ────────────────────────────────────────────────────────────

type taskCreate struct{ pool *pgxpool.Pool }

func (t *taskCreate) Name() string { return "task_create" }
func (t *taskCreate) Description() string {
	return "Create a todo on the dashboard. Source is set to 'agent' so the boss can see Jarvis filed it. Returns the new task id."
}
func (t *taskCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":    map[string]any{"type": "string", "description": "Short imperative ('Call insurance about claim')."},
			"body":     map[string]any{"type": "string", "description": "Optional notes."},
			"priority": map[string]any{"type": "string", "enum": []string{"low", "med", "high"}, "default": "med"},
			"due_at":   map[string]any{"type": "string", "description": "ISO 8601 timestamp. Optional."},
		},
		"required": []string{"title"},
	}
}
func (t *taskCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	title := strString(in, "title")
	if title == "" {
		return "", errors.New("title required")
	}
	priority := strDefault(in, "priority", "med")
	body := strString(in, "body")
	dueAt, _ := parseTime(in["due_at"])
	id := uuid.New()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_tasks (id, title, body, source, priority, status, due_at, created_at, updated_at)
		VALUES ($1, $2, $3, 'agent', $4, 'open', $5, NOW(), NOW())
	`, id, title, body, priority, dueAt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s"}`, id), nil
}

// ── task_update ────────────────────────────────────────────────────────────

type taskUpdate struct{ pool *pgxpool.Pool }

func (t *taskUpdate) Name() string { return "task_update" }
func (t *taskUpdate) Description() string {
	return "Update fields on a todo by id (title/body/priority/due_at/status). Only non-empty fields are applied."
}
func (t *taskUpdate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"title":    map[string]any{"type": "string"},
			"body":     map[string]any{"type": "string"},
			"priority": map[string]any{"type": "string", "enum": []string{"low", "med", "high"}},
			"due_at":   map[string]any{"type": "string"},
			"status":   map[string]any{"type": "string", "enum": []string{"open", "done", "dropped"}},
		},
		"required": []string{"id"},
	}
}
func (t *taskUpdate) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	// Build a dynamic UPDATE via COALESCE — keeps the SQL simple while
	// supporting partial updates without writing a builder.
	var (
		title, body, priority, status *string
		dueAt                         *time.Time
	)
	if v, ok := in["title"].(string); ok && v != "" {
		title = &v
	}
	if v, ok := in["body"].(string); ok {
		body = &v
	}
	if v, ok := in["priority"].(string); ok && v != "" {
		priority = &v
	}
	if v, ok := in["status"].(string); ok && v != "" {
		status = &v
	}
	if d, ok := parseTime(in["due_at"]); ok {
		dueAt = &d
	}
	doneAtClause := "done_at"
	if status != nil && *status == "done" {
		doneAtClause = "COALESCE(done_at, NOW())"
	}
	_, err := t.pool.Exec(ctx, `
		UPDATE mem_tasks
		   SET title    = COALESCE($2, title),
		       body     = COALESCE($3, body),
		       priority = COALESCE($4, priority),
		       status   = COALESCE($5, status),
		       due_at   = COALESCE($6, due_at),
		       done_at  = `+doneAtClause+`,
		       updated_at = NOW()
		 WHERE id = $1
	`, id, title, body, priority, status, dueAt)
	if err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// ── task_done ──────────────────────────────────────────────────────────────

type taskDone struct{ pool *pgxpool.Pool }

func (t *taskDone) Name() string        { return "task_done" }
func (t *taskDone) Description() string { return "Mark a todo done by id." }
func (t *taskDone) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}
func (t *taskDone) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	_, err := t.pool.Exec(ctx, `
		UPDATE mem_tasks SET status = 'done', done_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// ── pursuit_create ─────────────────────────────────────────────────────────

type pursuitCreate struct{ pool *pgxpool.Pool }

func (t *pursuitCreate) Name() string { return "pursuit_create" }
func (t *pursuitCreate) Description() string {
	return "Create a Pursuit (habit, weekly cadence, or long-term goal) with a cadence tag. Use cadence='daily'/'weekly' for habits (track via pursuit_checkin) or 'goal'/'quarterly' for objectives with progress targets."
}
func (t *pursuitCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":         map[string]any{"type": "string"},
			"cadence":       map[string]any{"type": "string", "enum": []string{"daily", "weekly", "goal", "quarterly"}, "default": "daily"},
			"target_value":  map[string]any{"type": "number", "description": "For goals: where you're trying to land (e.g. 24 for '24 books this year')."},
			"current_value": map[string]any{"type": "number", "description": "For goals: progress so far."},
			"unit":          map[string]any{"type": "string", "description": "books, %, lbs, sessions, …"},
			"due_at":        map[string]any{"type": "string", "description": "For goals: ISO 8601 target date."},
		},
		"required": []string{"title"},
	}
}
func (t *pursuitCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	title := strString(in, "title")
	if title == "" {
		return "", errors.New("title required")
	}
	cadence := strDefault(in, "cadence", "daily")
	var current, target *float64
	if v, ok := numFloat(in["current_value"]); ok {
		current = &v
	}
	if v, ok := numFloat(in["target_value"]); ok {
		target = &v
	}
	var unit *string
	if v, ok := in["unit"].(string); ok && v != "" {
		unit = &v
	}
	dueAt, _ := parseTime(in["due_at"])
	id := uuid.New()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_pursuits
			(id, title, cadence, current_value, target_value, unit, due_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`, id, title, cadence, current, target, unit, dueAt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s"}`, id), nil
}

// ── pursuit_checkin ────────────────────────────────────────────────────────

type pursuitCheckin struct{ pool *pgxpool.Pool }

func (t *pursuitCheckin) Name() string { return "pursuit_checkin" }
func (t *pursuitCheckin) Description() string {
	return "Record today's check-in for a daily/weekly Pursuit. Inserts the checkin row (idempotent per day) and updates the pursuit's streak + done_today markers. For progress-style goals, pass `delta` to increment current_value."
}
func (t *pursuitCheckin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pursuit_id": map[string]any{"type": "string"},
			"delta":      map[string]any{"type": "number", "description": "Optional progress increment for goal-style pursuits."},
			"note":       map[string]any{"type": "string"},
		},
		"required": []string{"pursuit_id"},
	}
}
func (t *pursuitCheckin) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "pursuit_id")
	if id == "" {
		return "", errors.New("pursuit_id required")
	}
	note := strString(in, "note")
	var delta *float64
	if v, ok := numFloat(in["delta"]); ok {
		delta = &v
	}

	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Idempotent upsert per (pursuit_id, day).
	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_pursuit_checkins (pursuit_id, day, checked_at, delta, note)
		VALUES ($1, CURRENT_DATE, NOW(), $2, $3)
		ON CONFLICT (pursuit_id, day) DO UPDATE
		   SET checked_at = EXCLUDED.checked_at,
		       delta      = COALESCE(EXCLUDED.delta, mem_pursuit_checkins.delta),
		       note       = COALESCE(NULLIF(EXCLUDED.note, ''), mem_pursuit_checkins.note)
	`, id, delta, note); err != nil {
		return "", err
	}

	// Streak: increment if yesterday was also checked, else reset to 1.
	// done_today is set true regardless.
	if _, err := tx.Exec(ctx, `
		UPDATE mem_pursuits SET
		    done_today  = true,
		    done_at     = NOW(),
		    streak_days = CASE
		        WHEN done_today THEN streak_days
		        WHEN EXISTS (
		            SELECT 1 FROM mem_pursuit_checkins
		            WHERE pursuit_id = $1 AND day = CURRENT_DATE - 1
		        ) THEN streak_days + 1
		        ELSE 1
		    END,
		    current_value = CASE
		        WHEN $2::numeric IS NOT NULL THEN COALESCE(current_value, 0) + $2::numeric
		        ELSE current_value
		    END,
		    updated_at = NOW()
		WHERE id = $1
	`, id, delta); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// ── followup_snooze ────────────────────────────────────────────────────────

type followupSnooze struct{ pool *pgxpool.Pool }

func (t *followupSnooze) Name() string { return "followup_snooze" }
func (t *followupSnooze) Description() string {
	return "Snooze a follow-up until a future time (default: 24h from now). Hidden from the dashboard until then."
}
func (t *followupSnooze) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "string"},
			"until": map[string]any{"type": "string", "description": "ISO 8601. Default: 24h from now."},
		},
		"required": []string{"id"},
	}
}
func (t *followupSnooze) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	until, ok := parseTime(in["until"])
	if !ok {
		until = time.Now().Add(24 * time.Hour)
	}
	_, err := t.pool.Exec(ctx, `
		UPDATE mem_followups SET status = 'snoozed', snoozed_until = $2, decided_at = NOW()
		WHERE id = $1
	`, id, until)
	if err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// ── followup_dismiss ───────────────────────────────────────────────────────

type followupDismiss struct{ pool *pgxpool.Pool }

func (t *followupDismiss) Name() string { return "followup_dismiss" }
func (t *followupDismiss) Description() string {
	return "Mark a follow-up as handled (the boss replied, or it's no longer relevant)."
}
func (t *followupDismiss) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}
func (t *followupDismiss) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	_, err := t.pool.Exec(ctx, `
		UPDATE mem_followups SET status = 'done', unread = false, decided_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// ── saved_add ──────────────────────────────────────────────────────────────

type savedAdd struct{ pool *pgxpool.Pool }

func (t *savedAdd) Name() string { return "saved_add" }
func (t *savedAdd) Description() string {
	return "Save an article, link, note, or quote to the boss's Saved shelf for later reference."
}
func (t *savedAdd) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":             map[string]any{"type": "string", "enum": []string{"article", "link", "note", "quote"}, "default": "note"},
			"title":            map[string]any{"type": "string"},
			"body":             map[string]any{"type": "string"},
			"url":              map[string]any{"type": "string"},
			"source_label":     map[string]any{"type": "string"},
			"reading_minutes":  map[string]any{"type": "integer"},
			"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"title"},
	}
}
func (t *savedAdd) Execute(ctx context.Context, in map[string]any) (string, error) {
	title := strString(in, "title")
	if title == "" {
		return "", errors.New("title required")
	}
	kind := strDefault(in, "kind", "note")
	body := strString(in, "body")
	var url, source *string
	if v, ok := in["url"].(string); ok && v != "" {
		url = &v
	}
	if v, ok := in["source_label"].(string); ok && v != "" {
		source = &v
	}
	var readingMinutes *int
	if v, ok := numFloat(in["reading_minutes"]); ok {
		i := int(v)
		readingMinutes = &i
	}
	var tagsJSON []byte
	if raw, ok := in["tags"].([]any); ok {
		tagsJSON, _ = json.Marshal(raw)
	}
	if tagsJSON == nil {
		tagsJSON = []byte("[]")
	}
	id := uuid.New()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_saved
			(id, kind, title, body, url, source_label, reading_minutes, tags, saved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, NOW())
	`, id, kind, title, body, url, source, readingMinutes, string(tagsJSON))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s"}`, id), nil
}

// ── helpers ────────────────────────────────────────────────────────────────

func strString(in map[string]any, k string) string {
	if v, ok := in[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func strDefault(in map[string]any, k, def string) string {
	if v := strString(in, k); v != "" {
		return v
	}
	return def
}

func numFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func parseTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
