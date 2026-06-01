// todo_write — the agent's own checklist for a background task.
//
// This is the generic building block behind the "Claude-Code-style todo list"
// the boss watches in the pinned dock. Per Rule #1, the cognition (what the
// steps ARE) lives with the model: it calls todo_write to declare and update an
// ordered checklist. The substrate just records it. The list lands in the
// CURRENT background run's mem_runs.meta.todos (a generic JSONB contract), and
// the dock renders it generically — no per-task Go, no bespoke column.
//
// Storage is keyed by run id resolved from the session→run binding
// (run_binding.go), so the tool works identically on the Mac and Cloud bridges:
// todo_write is native and always registered, and it never touches a bridge —
// it writes the same mem_runs row the background agent booked.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterTodoTools wires todo_write. No-op when pool is nil so chat-only
// deployments don't break.
func RegisterTodoTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&todoWrite{pool: pool})
}

type todoWrite struct{ pool *pgxpool.Pool }

func (t *todoWrite) Name() string   { return "todo_write" }
func (t *todoWrite) ReadOnly() bool { return false }

func (t *todoWrite) Description() string {
	return "Track your work on a multi-step background task with a live checklist the boss watches. " +
		"Call this ONCE at the very start with your full ordered plan (every item status 'pending'), then " +
		"call it again each time a step changes — mark the step you're starting 'in_progress' and finished " +
		"steps 'completed', exactly one item in_progress at a time. Each call REPLACES the whole list, so " +
		"always send the complete checklist. Optionally pass 'repo' (the repo/project you're working in) so " +
		"it pins at the top. Only meaningful inside a background_build run; skip it for a trivial one-step task."
}

func (t *todoWrite) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "The complete ordered checklist. Each call replaces the previous list.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":   map[string]any{"type": "string", "description": "Short imperative step, e.g. 'Wire the dock'."},
						"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
					},
					"required": []string{"text", "status"},
				},
			},
			"repo": map[string]any{"type": "string", "description": "Optional repo/project name to pin at the top of the checklist."},
		},
		"required": []string{"todos"},
	}
}

type todoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

func (t *todoWrite) Execute(ctx context.Context, in map[string]any) (string, error) {
	sid := SessionIDFromContext(ctx)
	runID := RunIDForSession(sid)
	if runID == "" {
		return "", errors.New("todo_write only works inside a background_build run — there's no run to attach the checklist to here")
	}

	raw, _ := in["todos"].([]any)
	todos := make([]todoItem, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(strString(m, "text"))
		if text == "" {
			continue
		}
		status := normalizeTodoStatus(strString(m, "status"))
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		todos = append(todos, todoItem{Text: text, Status: status})
		if len(todos) >= 50 {
			break // hard cap — a checklist longer than this is noise
		}
	}
	if len(todos) == 0 {
		return "", errors.New("todos must contain at least one {text, status} item")
	}

	done, total := 0, len(todos)
	current := ""
	for _, it := range todos {
		if it.Status == "completed" {
			done++
		}
		if it.Status == "in_progress" && current == "" {
			current = it.Text
		}
	}
	if current == "" {
		// No explicit in_progress: surface the first pending, else the last done.
		for _, it := range todos {
			if it.Status == "pending" {
				current = it.Text
				break
			}
		}
		if current == "" && total > 0 {
			current = todos[total-1].Text
		}
	}

	// completed/total, clamped so the bar never reads 0% or hits a full 100%
	// before Finish flips the row to ok.
	frac := float32(done) / float32(total)
	if frac < 0.02 {
		frac = 0.02
	}
	if frac > 0.98 {
		frac = 0.98
	}
	label := fmt.Sprintf("%d/%d - %s", done, total, current)

	// Build the meta patch: top-level merge (||) so we set todos (+ optional
	// repo) without clobbering currentFile the background loop writes.
	patch := map[string]any{"todos": todos}
	if repo := strings.TrimSpace(strString(in, "repo")); repo != "" {
		patch["repo"] = repo
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return "", err
	}

	_, err = t.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET meta           = COALESCE(meta,'{}'::jsonb) || $2::jsonb,
		       progress       = $3,
		       progress_label = $4
		 WHERE id = $1::uuid
	`, runID, patchJSON, frac, label)
	if err != nil {
		return "", err
	}
	MarkSessionHasTodos(sid)

	return fmt.Sprintf("checklist updated: %d/%d done, now: %s", done, total, current), nil
}

func normalizeTodoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "done":
		return "completed"
	case "in_progress", "in-progress", "active", "doing":
		return "in_progress"
	default:
		return "pending"
	}
}
