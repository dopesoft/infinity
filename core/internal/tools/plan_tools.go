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
	// Clean integer -> positional ref.
	if n, err := strconv.Atoi(raw); err == nil {
		return resolvePositionalStep(ctx, store, n)
	}
	// Not a clean integer. A real UUID always contains '-' (36-char hex-dashed);
	// if the ref has no dash but starts with a digit it's a mangled positional
	// emission like "2'}},{" from a sloppy tool call - recover the leading number.
	if !strings.Contains(raw, "-") && raw[0] >= '0' && raw[0] <= '9' {
		end := 0
		for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
			end++
		}
		if n, err := strconv.Atoi(raw[:end]); err == nil {
			return resolvePositionalStep(ctx, store, n)
		}
	}
	return raw, nil // treat as a real step id (uuid)
}

// planAdopter is the subset of *plan.Store the cross-session pickup needs. Kept
// as an interface so the plan_get/plan_update tools (which hold the narrower
// planGetter) can share the same seam.
type planAdopter interface {
	GetAnyActive(ctx context.Context) (*plan.Plan, error)
}

// anyActivePlanForTurn is THE cross-session plan-pickup seam. Every path that
// lets a turn reach a plan it doesn't own goes through here, so the two rules
// that govern it are stated once and can't drift apart:
//
//  1. AUTONOMOUS ONLY. Cron/heartbeat/background mint a fresh session UUID per
//     run and legitimately need to reach the plan they're executing. For
//     INTERACTIVE chat this fallback is the bug: a brand-new chat session with
//     no plan of its own would grab ANOTHER session's active plan and start
//     mutating it (the 2026-07-02 bleed onto c03495f5). Interactive → no
//     fallback; a missing plan correctly forces plan_create.
//
//  2. PICKING IT UP MEANS OWNING IT. The session now driving the plan is
//     re-stamped onto it (AdoptSession). Without this the plan keeps pointing at
//     a dead session and every session-keyed consumer downstream silently
//     no-ops: the cron finalize settles nothing, the outcome classifier's
//     incomplete-plan backstop sees nil and reports "did_work", and the plan
//     spins as a phantom "Running" card forever. That is exactly what happened
//     to the weekly AI digest on 2026-07-27 — retry attempt 2 finished the work
//     while attempt 1 still owned the plan (see plan.Store.AdoptSession).
//
// Best-effort adoption: a failed re-stamp logs nothing and still returns the
// plan, because losing the bookkeeping is strictly better than failing the
// agent's tool call.
func anyActivePlanForTurn(ctx context.Context, store planAdopter) (*plan.Plan, error) {
	if store == nil || !IsAutonomous(ctx) {
		return nil, nil
	}
	p, err := store.GetAnyActive(ctx)
	if err != nil || p == nil {
		return nil, err
	}
	if adopter, ok := store.(interface {
		AdoptSession(ctx context.Context, planID, sessionID string) error
	}); ok {
		_ = adopter.AdoptSession(ctx, p.ID, SessionIDFromContext(ctx))
	}
	return p, nil
}

// resolvePositionalStep maps a 1-based step number to the active plan's step id.
// It first looks up the active plan for the current session; when that yields
// nothing (cross-session continuation, cron with a new session ID, or a context
// with no session at all) it falls back to the most recently updated active/paused
// plan so a positional ref like "5" never spuriously fails just because the caller
// didn't call plan_get first in this session.
func resolvePositionalStep(ctx context.Context, store *plan.Store, n int) (string, error) {
	p, err := store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	if p == nil {
		p, err = anyActivePlanForTurn(ctx, store)
		if err != nil {
			return "", err
		}
	}
	if p == nil || len(p.Steps) == 0 {
		return "", fmt.Errorf("there's no active plan to resolve step %d against — create one with plan_create first", n)
	}
	if n < 1 || n > len(p.Steps) {
		return "", fmt.Errorf("step %d is out of range - this plan has %d steps (use 1..%d)", n, len(p.Steps), len(p.Steps))
	}
	return p.Steps[n-1].ID, nil // Steps come back ordered idx ASC.
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
	r.Register(&planRevise{store: store})
	r.Register(&planCancel{store: store})
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
	goal := strString(in, "goal")
	// A plan launched by a named job (a cron) inherits the JOB's name as its
	// headline so the cron, the card, and the plan all read the same thing —
	// instead of the model inventing a fresh title and the board showing one job
	// under two names. The model's descriptive title is kept as the goal so it
	// still rides in the card detail. Ordinary boss-chat plans (no job binding)
	// keep the model's title untouched.
	if job := JobForSession(sid); job != "" {
		if strings.TrimSpace(goal) == "" {
			goal = title
		}
		title = humanizeJobName(job)
	}
	p, err := t.store.Create(ctx, sid, title, goal, strString(in, "goal_id"), steps)
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
		// Step ID is stale (plan was recreated, session switched, or context
		// compacted). Include the current active plan so the model sees the
		// valid step IDs and can retry with the right ref immediately — no
		// separate plan_get call needed (Rule #1b: mechanic, not prose).
		p, _ := t.store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
		if p == nil {
			p, _ = anyActivePlanForTurn(ctx, t.store)
		}
		if p != nil {
			b, _ := json.Marshal(map[string]any{
				"error":       fmt.Sprintf("no plan step with id %s — the step list may be stale", stepID),
				"active_plan": p,
				"hint":        "Use a step id or 1-based position from active_plan.steps above.",
			})
			return string(b), nil
		}
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
	stepID, err := resolveStepRef(ctx, t.store, strString(in, "step_id"))
	if err != nil {
		return "", err
	}
	verdict := strings.ToLower(strings.TrimSpace(strString(in, "verdict")))
	evidence := strString(in, "evidence")
	if verdict == "" || evidence == "" {
		return "", errors.New("verdict and evidence are required")
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

// planGetter is the minimal store interface planGet needs. A dedicated interface
// (instead of the concrete *plan.Store) lets unit tests inject a fake without a
// real DB while every other plan tool continues using the concrete type unchanged.
type planGetter interface {
	Get(ctx context.Context, planID string) (*plan.Plan, error)
	GetActiveBySession(ctx context.Context, sessionID string) (*plan.Plan, error)
	GetAnyActive(ctx context.Context) (*plan.Plan, error)
	// AdoptSession is needed for cross-session plan resume: when plan_get is
	// called with an explicit plan_id from a foreign session, it must re-anchor
	// the plan onto the current session so subsequent plan_update/plan_verify
	// positional step refs ("2") resolve against it. See plan_get.Execute.
	AdoptSession(ctx context.Context, planID, sessionID string) error
}

// ── plan_get ─────────────────────────────────────────────────────────────

type planGet struct{ store planGetter }

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
		// Re-anchor the plan onto the current session so subsequent
		// plan_update/plan_verify calls with positional step refs ("2") can
		// resolve against it via GetActiveBySession. Without this, the plan's
		// session_id still points at the original session, so resolvePositionalStep
		// finds nothing for the current session and throws "no active plan to
		// resolve step N against". Best-effort: a failed adopt still returns the
		// plan (showing it is strictly better than a hard error here).
		if p != nil {
			if sid := SessionIDFromContext(ctx); sid != "" && p.SessionID != sid {
				_ = t.store.AdoptSession(ctx, id, sid)
				p.SessionID = sid // reflect locally so callers see the new owner
			}
		}
		return renderPlan(p), nil
	}
	p, err := t.store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	// Cross-session pickup (autonomous only, and it adopts the plan onto this
	// session). This is how a cron RETRY reaches the plan its previous attempt
	// created. See anyActivePlanForTurn.
	if p == nil {
		p, err = anyActivePlanForTurn(ctx, t.store)
		if err != nil {
			return "", err
		}
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

// ── plan_revise ──────────────────────────────────────────────────────────
//
// In-place plan editing for when work DIVERTS from the plan: prune steps that
// no longer apply, rewrite a step to what you're actually doing now, add new
// steps. This is the honest alternative to faking a step done or nuking the
// whole plan with plan_create - the steps already finished stay finished.

type planRevise struct{ store *plan.Store }

func (t *planRevise) Name() string { return "plan_revise" }
func (t *planRevise) Description() string {
	return "Revise the current plan IN PLACE when the work diverts from it - the right move instead of " +
		"faking a step done or starting over. `edit` rewrites a step's title/detail (e.g. step 2 was " +
		"'Install X' but it failed, so repurpose it to 'Clean up the failed install'); `remove` prunes " +
		"steps that no longer apply (e.g. drop the last 2); `add` appends new steps. Steps already done " +
		"stay done. Refer to a step by its 1-based number or its id. Operates on this session's active " +
		"plan unless plan_id is given. To kill a plan entirely, use plan_cancel instead."
}
func (t *planRevise) Schema() map[string]any {
	stepRef := map[string]any{"type": "string", "description": "Step's 1-based number (e.g. \"2\") or its id."}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "Optional: a specific plan id. Omit for this session's active plan."},
			"edit": map[string]any{
				"type":        "array",
				"description": "Rewrite existing steps.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"step":   stepRef,
						"title":  map[string]any{"type": "string", "description": "New title (omit to keep)."},
						"detail": map[string]any{"type": "string", "description": "New detail (omit to keep)."},
					},
					"required": []string{"step"},
				},
			},
			"remove": map[string]any{
				"type":        "array",
				"description": "Prune these steps (by number or id).",
				"items":       map[string]any{"type": "string"},
			},
			"add": map[string]any{
				"type":        "array",
				"description": "Append new steps to the end.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":           map[string]any{"type": "string"},
						"detail":          map[string]any{"type": "string"},
						"is_checkpoint":   map[string]any{"type": "boolean"},
						"verify_required": map[string]any{"type": "boolean"},
					},
					"required": []string{"title"},
				},
			},
		},
	}
}
func (t *planRevise) Execute(ctx context.Context, in map[string]any) (string, error) {
	// Snapshot the plan FIRST so positional refs resolve against the pre-edit
	// order (prunes shift positions, so all refs must be resolved up front).
	var (
		p   *plan.Plan
		err error
	)
	if id := strings.TrimSpace(strString(in, "plan_id")); id != "" {
		p, err = t.store.Get(ctx, id)
	} else {
		p, err = t.store.GetActiveBySession(ctx, SessionIDFromContext(ctx))
	}
	if err != nil {
		return "", err
	}
	if p == nil {
		return "", errors.New("no active plan to revise - create one with plan_create first")
	}

	resolve := func(ref string) (string, error) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return "", errors.New("empty step reference")
		}
		if n, e := strconv.Atoi(ref); e == nil {
			if n < 1 || n > len(p.Steps) {
				return "", fmt.Errorf("step %d is out of range (this plan has %d steps)", n, len(p.Steps))
			}
			return p.Steps[n-1].ID, nil
		}
		for i := range p.Steps {
			if p.Steps[i].ID == ref {
				return ref, nil
			}
		}
		return "", fmt.Errorf("no step %q in this plan", ref)
	}

	// Resolve every ref up front against the snapshot.
	type editOp struct{ id, title, detail string }
	var edits []editOp
	for _, raw := range arrOf(in, "edit") {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sid, e := resolve(strString(m, "step"))
		if e != nil {
			return "", e
		}
		edits = append(edits, editOp{id: sid, title: strString(m, "title"), detail: strString(m, "detail")})
	}
	var removeIDs []string
	for _, raw := range arrOf(in, "remove") {
		ref, _ := raw.(string)
		sid, e := resolve(ref)
		if e != nil {
			return "", e
		}
		removeIDs = append(removeIDs, sid)
	}
	var adds []plan.NewStepInput
	for _, raw := range arrOf(in, "add") {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := strings.TrimSpace(strString(m, "title"))
		if title == "" {
			continue
		}
		adds = append(adds, plan.NewStepInput{
			Title:          title,
			Detail:         strString(m, "detail"),
			IsCheckpoint:   boolOrFalse(m, "is_checkpoint"),
			VerifyRequired: boolOrFalse(m, "verify_required"),
		})
	}
	if len(edits) == 0 && len(removeIDs) == 0 && len(adds) == 0 {
		return "", errors.New("nothing to revise - pass edit, remove, and/or add")
	}

	// Apply by id (order-independent now that refs are resolved).
	for _, e := range edits {
		if err := t.store.EditStep(ctx, e.id, e.title, e.detail); err != nil {
			return "", err
		}
	}
	for _, sid := range removeIDs {
		if _, err := t.store.RemoveStep(ctx, sid); err != nil {
			return "", err
		}
	}
	for _, a := range adds {
		if err := t.store.AppendStep(ctx, p.ID, a); err != nil {
			return "", err
		}
	}
	if err := t.store.RenumberSteps(ctx, p.ID); err != nil {
		return "", err
	}
	if err := t.store.Recompute(ctx, p.ID); err != nil {
		return "", err
	}
	refreshed, err := t.store.Get(ctx, p.ID)
	if err != nil {
		return "", err
	}
	return renderPlan(refreshed), nil
}

// ── plan_cancel ──────────────────────────────────────────────────────────

type planCancel struct{ store *plan.Store }

func (t *planCancel) Name() string { return "plan_cancel" }
func (t *planCancel) Description() string {
	return "Kill the current plan when the boss tells you to drop / kill / cancel / stop / abandon it, or " +
		"when it genuinely can't succeed. This CANCELS the plan (status=cancelled, kept for history) so it " +
		"stops driving your work, leaves your context, and clears off the boss's dashboard. Cancels this " +
		"session's active plan by default; pass plan_id for a specific one. NEVER fake a kill by marking " +
		"steps 'done' - 'done' means the step actually succeeded. If you've diverted but aren't killing the " +
		"whole plan, use plan_revise to prune/rewrite steps instead."
}
func (t *planCancel) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "Optional: a specific plan id. Omit to cancel this session's active plan."},
		},
	}
}
func (t *planCancel) Execute(ctx context.Context, in map[string]any) (string, error) {
	var (
		p   *plan.Plan
		err error
	)
	if id := strings.TrimSpace(strString(in, "plan_id")); id != "" {
		p, err = t.store.Cancel(ctx, id)
	} else {
		p, err = t.store.CancelActive(ctx, SessionIDFromContext(ctx))
	}
	if err != nil {
		return "", err
	}
	if p == nil {
		out, _ := json.Marshal(map[string]any{"plan": nil, "note": "There's no active plan to cancel."})
		return string(out), nil
	}
	// Close any live spinner left on an in-progress step so the dashboard
	// doesn't dangle a running indicator after the plan is gone.
	for i := range p.Steps {
		if p.Steps[i].Status == plan.StepInProgress && p.Steps[i].RunID != "" {
			runs.FinishByID(ctx, p.Steps[i].RunID, nil, "plan cancelled")
		}
	}
	out, _ := json.Marshal(map[string]any{"plan": p, "note": "Plan cancelled - it's off the board and out of your active context."})
	return string(out), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// arrOf returns the []any stored at key (nil when absent or the wrong type).
func arrOf(in map[string]any, key string) []any {
	v, _ := in[key].([]any)
	return v
}

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
