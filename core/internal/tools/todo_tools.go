// todo_write — the quick-checklist entry point onto the durable plan substrate.
//
// UNIFICATION: a todo checklist and a plan are the SAME thing. todo_write is the
// flat, lightweight way to lay one out (every step a plain {text, status});
// plan_create is the richer way (per-step verification + checkpoints). Both
// write the ONE durable concept — mem_plans / mem_plan_steps — so the boss sees
// the identical thing whether he's in chat (the pinned dock) or on the dashboard
// (the Agent Work board's plan card). No second store, no mem_runs.meta.todos
// copy to drift.
//
// Session binding (run_binding.go): when called inside a detached
// background_build CHILD session, the plan is bound to the PARENT chat session
// that kicked the build off, so it surfaces in the boss's conversation + the
// dashboard rather than orphaning on a throwaway child id. Called directly in a
// normal chat, it just writes the current session's plan. Bridge-agnostic and
// always registered, so it works on Mac and Cloud alike.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterTodoTools wires todo_write. No-op when pool is nil so chat-only
// deployments don't break.
func RegisterTodoTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&todoWrite{pool: pool, plans: plan.NewStore(pool)})
}

type todoWrite struct {
	pool  *pgxpool.Pool
	plans *plan.Store
}

func (t *todoWrite) Name() string   { return "todo_write" }
func (t *todoWrite) ReadOnly() bool { return false }

func (t *todoWrite) Description() string {
	return "Lay out and update a live checklist the boss watches in chat AND on the dashboard - it's the " +
		"quick form of a plan (use plan_create instead when steps need verification or a checkpoint). " +
		"Call this ONCE at the start with your full ordered list (every item status 'pending'), then call it " +
		"again each time a step changes - mark the step you're starting 'in_progress' and finished steps " +
		"'completed', exactly one in_progress at a time. Each call REPLACES the whole list, so always send the " +
		"complete checklist. Optionally pass 'repo' (the project you're working in) as the checklist title. " +
		"Skip it for a trivial one-step task."
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
		return "", fmt.Errorf("todos must contain at least one {text, status} item")
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

	// Bind the plan to the boss's chat session: inside a detached background
	// build, sid is the throwaway child — write the plan to the PARENT so it
	// shows in his conversation + the dashboard, not on the orphan child.
	planSession := sid
	if parent := ParentSessionForSession(sid); parent != "" {
		planSession = parent
	}

	items := make([]plan.ChecklistItem, 0, len(todos))
	for _, it := range todos {
		items = append(items, plan.ChecklistItem{Text: it.Text, Status: it.Status})
	}
	title := strings.TrimSpace(strString(in, "repo"))
	if _, err := t.plans.SyncChecklist(ctx, planSession, title, items); err != nil {
		return "", err
	}

	// Keep the background run's progress + current-step telemetry live for the
	// dock's spinner and the run row, when this is running inside a bound build.
	// The checklist itself now lives in the plan (single source) - we only push
	// the % / label here, no duplicate todo list on mem_runs.
	if runID := RunIDForSession(sid); runID != "" {
		frac := float32(done) / float32(total)
		if frac < 0.02 {
			frac = 0.02
		}
		if frac > 0.98 {
			frac = 0.98
		}
		label := fmt.Sprintf("%d/%d - %s", done, total, current)
		meta := map[string]any{}
		if title != "" {
			meta["repo"] = title
		}
		metaJSON, _ := json.Marshal(meta)
		_, _ = t.pool.Exec(ctx, `
			UPDATE mem_runs
			   SET meta           = COALESCE(meta,'{}'::jsonb) || $2::jsonb,
			       progress       = $3,
			       progress_label = $4
			 WHERE id = $1::uuid
		`, runID, string(metaJSON), frac, label)
		MarkSessionHasTodos(sid)
	}

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
