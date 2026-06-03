// plan_tools.go - the agent's durable, steerable plan ("the Cortex").
//
// Per Rule #1, the cognition (what the steps ARE, when to verify, how to
// replan) lives with the model and the seeded planning skill. The substrate
// just records the plan and how each step went. Unlike todo_write (which is
// ephemeral, tied to a background run, and dies on restart), a plan lives in
// mem_plans / mem_plan_steps and is re-injected every turn by PlanProvider, so
// a long task resumes from saved step state after compaction / restart / a
// fresh session.
//
// The self-verification reflex is enforced STRUCTURALLY, not just by prompt: a
// verify_required step cannot be marked done until plan_verify records a
// passing verdict. A failing verdict flips the step to 'blocked' so the agent
// replans. Each in-flight step books a mem_runs row (kind 'plan.step') so the
// dashboard shows a navigation-proof live spinner per step.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveStepRef turns the model's step reference into a real step UUID. The
// runtime LLM routinely addresses a step positionally ("2") instead of by its
// opaque UUID, which would otherwise blow up as a raw Postgres 22P02. Per Rule
// #1b this is a mechanic, so it's enforced in code, not asked for in prose: a
// UUID is used directly; a bare integer N resolves to the 1-based position in
// this session's active plan (step 1 = first). Steps come back ordered idx ASC.
func resolveStepRef(ctx context.Context, store *plan.Store, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("step_id required")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return raw, nil // not an integer -> treat as a real step id (uuid)
	}
	p, perr := store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
	if perr != nil {
		return "", perr
	}
	if p == nil || len(p.Steps) == 0 {
		return "", fmt.Errorf("there's no active plan to resolve step %d against - call plan_get first", n)
	}
	if n < 1 || n > len(p.Steps) {
		return "", fmt.Errorf("step %d is out of range - this plan has %d steps (use 1..%d)", n, len(p.Steps), len(p.Steps))
	}
	return p.Steps[n-1].ID, nil
}

// RegisterPlanTools wires the plan substrate tools. No-op when pool is nil so
// chat-only deployments don't break.
func RegisterPlanTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	store := plan.NewStore(pool)
	r.Register(&planCreate{store: store})
	r.Register(&planUpdate{store: store})
	r.Register(&planVerify{store: store})
	r.Register(&planGet{store: store})
	r.Register(&planList{store: store})
}

// renderPlan is the compact JSON the tools hand back so the model sees the
// current state after every mutation.
func renderPlan(p *plan.Plan) string {
	if p == nil {
		out, _ := json.Marshal(map[string]any{"plan": nil})
		return string(out)
	}
	b, _ := json.Marshal(map[string]any{"plan": p})
	return string(b)
}

// ── plan_create ──────────────────────────────────────────────────────────

type planCreate struct{ store *plan.Store }

func (t *planCreate) Name() string { return "plan_create" }
func (t *planCreate) Description() string {
	return "Lay out a durable, step-by-step plan for a multi-step task. Call this FIRST for any task " +
		"with 3+ steps or that spans multiple tool calls. The plan survives compaction, restart, and " +
		"session boundaries, and the boss can watch it on the dashboard. Mark a step is_checkpoint=true " +
		"when you should pause for the boss's approval before continuing; mark verify_required=true when " +
		"the step must be proven (file exists / test passed / API 200) before it counts as done. Creating " +
		"a new plan supersedes any prior active plan for this session. Then drive it with plan_update / " +
		"plan_verify as you work."
}
func (t *planCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "Short title for the plan, e.g. 'Ship the weekly digest'."},
			"goal":  map[string]any{"type": "string", "description": "One sentence: what success looks like."},
			"steps": map[string]any{
				"type":        "array",
				"description": "The ordered checklist. Keep each step concrete and verifiable.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":           map[string]any{"type": "string", "description": "Short imperative step, e.g. 'Pull the last 7 days of email'."},
						"detail":          map[string]any{"type": "string", "description": "Optional extra context for the step."},
						"is_checkpoint":   map[string]any{"type": "boolean", "description": "Pause for the boss's approval when the plan reaches this step."},
						"verify_required": map[string]any{"type": "boolean", "description": "This step must pass plan_verify before it can be marked done."},
					},
					"required": []string{"title"},
				},
			},
			"goal_id": map[string]any{"type": "string", "description": "Optional mem_agent_goals id to link a multi-day plan to a durable objective."},
		},
		"required": []string{"title", "steps"},
	}
}
func (t *planCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	title := strings.TrimSpace(strString(in, "title"))
	if title == "" {
		return "", errors.New("title required")
	}
	rawSteps, _ := in["steps"].([]any)
	steps := make([]plan.NewStepInput, 0, len(rawSteps))
	for _, v := range rawSteps {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		st := strings.TrimSpace(strString(m, "title"))
		if st == "" {
			continue
		}
		steps = append(steps, plan.NewStepInput{
			Title:          st,
			Detail:         strString(m, "detail"),
			IsCheckpoint:   boolOrFalse(m, "is_checkpoint"),
			VerifyRequired: boolOrFalse(m, "verify_required"),
		})
	}
	if len(steps) == 0 {
		return "", errors.New("steps must contain at least one {title} item")
	}
	sid := SessionIDFromContext(ctx)
	p, err := t.store.Create(ctx, sid, title, strString(in, "goal"), strString(in, "goal_id"), steps)
	if err != nil {
		return "", err
	}
	return renderPlan(p), nil
}

// ── plan_update ──────────────────────────────────────────────────────────

type planUpdate struct{ store *plan.Store }

func (t *planUpdate) Name() string { return "plan_update" }
func (t *planUpdate) Description() string {
	return "Advance a plan step. Set status to 'in_progress' when you START a step and 'done' when you " +
		"FINISH it (exactly one step in_progress at a time). Use 'skipped' to drop a step, 'failed' if it " +
		"can't be completed. A step with verify_required CANNOT be marked done until plan_verify records a " +
		"passing verdict - verify first. Pass result_summary to record how the step went; it shows in the " +
		"step timeline. Starting a step books a live spinner the boss sees; finishing it closes it."
}
func (t *planUpdate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":        map[string]any{"type": "string", "description": "The step's id from plan_create / plan_get, OR its 1-based number in the plan (e.g. \"2\" for the second step)."},
			"status":         map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done", "skipped", "failed"}},
			"result_summary": map[string]any{"type": "string", "description": "Short note on how the step went."},
		},
		"required": []string{"step_id", "status"},
	}
}
func (t *planUpdate) Execute(ctx context.Context, in map[string]any) (string, error) {
	stepID, err := resolveStepRef(ctx, t.store, strString(in, "step_id"))
	if err != nil {
		return "", err
	}
	status := plan.NormalizeStepStatus(strString(in, "status"))
	summary := strString(in, "result_summary")

	prev, err := t.store.GetStep(ctx, stepID)
	if err != nil {
		return "", err
	}
	if prev == nil {
		return "", fmt.Errorf("no plan step with id %s", stepID)
	}

	// Structural self-verification guard: a verify_required step can't be
	// declared done until plan_verify has recorded a passing verdict. This is
	// the verify-before-done reflex enforced by the substrate, not just the
	// prompt.
	if status == plan.StepDone && prev.VerifyRequired {
		verdict, _ := prev.VerifyResult["verdict"].(string)
		if verdict != "pass" {
			return "", fmt.Errorf("step %q requires verification before it can be marked done - call plan_verify with the evidence it actually worked first", prev.Title)
		}
	}

	// Book a live spinner when a step starts; close it when it ends.
	if status == plan.StepInProgress && prev.Status != plan.StepInProgress {
		h := runs.BeginGlobal(ctx, runs.KindPlanStep, stepID, "Plan step: "+truncForLabel(prev.Title), runs.SourceAgent)
		if rid := h.ID(); rid != "" {
			_ = t.store.SetStepRun(ctx, stepID, rid)
		}
	}

	p, err := t.store.MarkStep(ctx, stepID, status, summary)
	if err != nil {
		return "", err
	}

	if status == plan.StepDone || status == plan.StepSkipped || status == plan.StepFailed {
		if prev.RunID != "" {
			var runErr error
			if status == plan.StepFailed {
				runErr = errors.New(firstNonEmpty(summary, "step failed"))
			}
			runs.FinishByID(ctx, prev.RunID, runErr, summary)
		}
	}
	return renderPlan(p), nil
}

// ── plan_verify ──────────────────────────────────────────────────────────

type planVerify struct{ store *plan.Store }

func (t *planVerify) Name() string { return "plan_verify" }
func (t *planVerify) Description() string {
	return "Record whether a step actually worked, with the evidence. A step is only truly done when you " +
		"can point to proof: the file exists, the test passed, the API returned 200, the output matches " +
		"intent. Pass verdict='pass' with the evidence to clear a verify_required step for plan_update " +
		"done; pass verdict='fail' to flag it - the step flips to 'blocked' and you should replan it."
}
func (t *planVerify) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id":  map[string]any{"type": "string", "description": "The step's id from plan_get, OR its 1-based number in the plan (e.g. \"2\" for the second step)."},
			"verdict":  map[string]any{"type": "string", "enum": []string{"pass", "fail"}},
			"evidence": map[string]any{"type": "string", "description": "The concrete proof you checked, e.g. 'go build ./... exited 0', 'GET /health -> 200', 'file dist/app.js exists, 4.2kb'."},
			"method":   map[string]any{"type": "string", "description": "Optional: how you checked."},
		},
		"required": []string{"step_id", "verdict", "evidence"},
	}
}
func (t *planVerify) Execute(ctx context.Context, in map[string]any) (string, error) {
	stepID := strings.TrimSpace(strString(in, "step_id"))
	verdict := strings.ToLower(strings.TrimSpace(strString(in, "verdict")))
	evidence := strString(in, "evidence")
	if stepID == "" || verdict == "" || evidence == "" {
		return "", errors.New("step_id, verdict, and evidence are all required")
	}
	p, err := t.store.RecordVerify(ctx, stepID, verdict, evidence, strString(in, "method"))
	if err != nil {
		return "", err
	}
	if verdict == "fail" {
		out, _ := json.Marshal(map[string]any{
			"plan": p,
			"note": "verification failed - this step is now blocked. Diagnose why, then replan: either redo the step and re-verify, or revise the plan.",
		})
		return string(out), nil
	}
	return renderPlan(p), nil
}

// ── plan_get ─────────────────────────────────────────────────────────────

type planGet struct{ store *plan.Store }

func (t *planGet) Name() string   { return "plan_get" }
func (t *planGet) ReadOnly() bool { return true }
func (t *planGet) Description() string {
	return "Fetch the current active plan for this session (with all steps + their status), or a specific " +
		"plan by id. Use it to re-read where you are before continuing a multi-step task."
}
func (t *planGet) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "Optional: a specific plan id. Omit to get this session's active plan."},
		},
	}
}
func (t *planGet) Execute(ctx context.Context, in map[string]any) (string, error) {
	if id := strings.TrimSpace(strString(in, "plan_id")); id != "" {
		p, err := t.store.Get(ctx, id)
		if err != nil {
			return "", err
		}
		return renderPlan(p), nil
	}
	p, err := t.store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	return renderPlan(p), nil
}

// ── plan_list ────────────────────────────────────────────────────────────

type planList struct{ store *plan.Store }

func (t *planList) Name() string   { return "plan_list" }
func (t *planList) ReadOnly() bool { return true }
func (t *planList) Description() string {
	return "List recent plans (active and completed) with their steps. Use it to review what you've been " +
		"working on across sessions."
}
func (t *planList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "description": "Optional filter: active | paused | completed | failed | cancelled. Default returns active + paused."},
			"limit":  map[string]any{"type": "integer", "default": 25},
		},
	}
}
func (t *planList) Execute(ctx context.Context, in map[string]any) (string, error) {
	statuses := []string{plan.PlanActive, plan.PlanPaused}
	if s := strings.TrimSpace(strString(in, "status")); s != "" {
		statuses = []string{s}
	}
	plans, err := t.store.ListByStatuses(ctx, statuses, intOrZero(in, "limit"))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"count": len(plans), "plans": plans})
	return string(out), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncForLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
