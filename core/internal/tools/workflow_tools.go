// Workflow tools - durable, multi-step workflows, agent-facing.
//
// Phase 2 of the assembly substrate. A workflow is how the agent turns a
// complex natural-language request - "every morning pull my calendar,
// draft prep for each meeting, surface them" - into a durable process that
// survives restarts. workflow_create saves a reusable definition;
// workflow_run starts an execution; the engine advances it step by step.
//
// These tools are the contract. The agent never writes the mem_workflow*
// tables with raw SQL.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/workflow"
)

// RegisterWorkflowTools wires the six workflow management tools. No-op when
// pool is nil so chat-only / no-DB deployments don't break registration.
func RegisterWorkflowTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	store := workflow.NewStore(pool, nil)
	r.Register(&workflowCreateTool{store: store})
	r.Register(&workflowRunTool{store: store})
	r.Register(&workflowStatusTool{store: store})
	r.Register(&workflowResumeTool{store: store})
	r.Register(&workflowCancelTool{store: store})
	r.Register(&workflowListTool{store: store})
	r.Register(&workflowValidateTool{store: store})
}

// stepsParamSchema is the shared JSON schema for the step list - the heart
// of the workflow contract.
func stepsParamSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"description": "Ordered steps the engine runs as a state machine. Each step's `spec` " +
			"depends on `kind` - tool: {\"tool\":\"<name>\",\"args\":{…}}; skill: " +
			"{\"skill\":\"<name>\",\"args\":{…}}; agent: {\"prompt\":\"…\"}; checkpoint: " +
			"{\"message\":\"…\"}. String values in a spec may reference earlier results with " +
			"{{steps.N.output}} or run inputs with {{input.KEY}}.",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "Short label for the step."},
				"kind": map[string]any{
					"type": "string",
					"enum": []string{"tool", "skill", "agent", "checkpoint"},
					"description": "tool = call a native/MCP tool · skill = invoke a skill · " +
						"agent = run a sub-agent turn · checkpoint = pause for boss approval.",
				},
				"spec":         map[string]any{"type": "object", "description": "Step parameters - shape depends on kind (see parent description)."},
				"max_attempts": map[string]any{"type": "integer", "description": "Retries before the step (and run) fails. Default 3."},
			},
			"required": []string{"kind", "spec"},
		},
	}
}

func parseSteps(raw any) ([]workflow.StepDef, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, errors.New("steps must be an array")
	}
	if len(arr) == 0 {
		return nil, errors.New("steps must not be empty")
	}
	out := make([]workflow.StepDef, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("step %d must be an object", i)
		}
		kind := workflow.StepKind(strings.TrimSpace(fmt.Sprint(m["kind"])))
		if !kind.Valid() {
			return nil, fmt.Errorf("step %d: invalid kind %q (want tool|skill|agent|checkpoint)", i, kind)
		}
		spec, _ := m["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		name, _ := m["name"].(string)
		maxAttempts := 0
		if v, ok := m["max_attempts"].(float64); ok {
			maxAttempts = int(v)
		}
		out = append(out, workflow.StepDef{Name: name, Kind: kind, Spec: spec, MaxAttempts: maxAttempts})
	}
	return out, nil
}

// ── workflow_create ─────────────────────────────────────────────────────────

type workflowCreateTool struct{ store *workflow.Store }

func (t *workflowCreateTool) Name() string { return "workflow_create" }
func (t *workflowCreateTool) Description() string {
	return "Save a reusable workflow - a named, ordered list of steps the engine " +
		"runs as a durable state machine. Use this when the boss describes a " +
		"multi-step process worth keeping. Each step is one of: tool (call a " +
		"native/MCP tool), skill (invoke a skill), agent (run a sub-agent turn " +
		"with a prompt), or checkpoint (pause for the boss to approve before " +
		"continuing). Steps chain - a later step's spec can reference " +
		"{{steps.N.output}}. Returns the workflow id; run it with workflow_run."
}
func (t *workflowCreateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "kebab-case workflow name (e.g. 'morning-brief')."},
			"description": map[string]any{"type": "string", "description": "One or two sentences: what the workflow does."},
			"steps":       stepsParamSchema(),
		},
		"required": []string{"name", "steps"},
	}
}
func (t *workflowCreateTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strings.TrimSpace(strString(in, "name"))
	if name == "" {
		return "", errors.New("workflow_create: name is required")
	}
	steps, err := parseSteps(in["steps"])
	if err != nil {
		return "", fmt.Errorf("workflow_create: %w", err)
	}
	wf := &workflow.Workflow{
		Name:        name,
		Description: strString(in, "description"),
		Steps:       steps,
		Source:      "agent",
	}
	id, err := t.store.UpsertWorkflow(ctx, wf)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "id": id, "name": name, "steps": len(steps),
		"message": fmt.Sprintf("Workflow %q saved (%d steps). Run it with workflow_run.", name, len(steps)),
	})
	return string(out), nil
}

// ── workflow_run ────────────────────────────────────────────────────────────

type workflowRunTool struct{ store *workflow.Store }

func (t *workflowRunTool) Name() string { return "workflow_run" }
func (t *workflowRunTool) Description() string {
	return "Start a workflow run. Pass `workflow` (the name of a saved workflow) " +
		"OR an inline `steps` list for a one-off. The run starts immediately and " +
		"the engine advances it step by step, surviving restarts. Pass `input` " +
		"for run-level values steps can reference with {{input.KEY}}. Returns the " +
		"run id - poll it with workflow_status."
}
func (t *workflowRunTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workflow":   map[string]any{"type": "string", "description": "Name of a saved workflow to run. Omit if passing inline steps."},
			"name":       map[string]any{"type": "string", "description": "Label for an ad-hoc run (used when passing inline steps)."},
			"steps":      stepsParamSchema(),
			"input":      map[string]any{"type": "object", "description": "Run-level inputs, referenceable in step specs as {{input.KEY}}."},
			"depends_on": map[string]any{"type": "string", "description": "Optional run id - hold this run until that run is done (dependency-aware scheduling)."},
		},
	}
}
func (t *workflowRunTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	input, _ := in["input"].(map[string]any)
	wfName := strings.TrimSpace(strString(in, "workflow"))

	var (
		steps      []workflow.StepDef
		workflowID string
		runName    string
		err        error
	)
	if wfName != "" {
		wf, gerr := t.store.GetWorkflow(ctx, wfName)
		if gerr != nil {
			return "", gerr
		}
		if wf == nil {
			return "", fmt.Errorf("workflow_run: no saved workflow named %q", wfName)
		}
		steps, workflowID, runName = wf.Steps, wf.ID, wf.Name
	} else {
		steps, err = parseSteps(in["steps"])
		if err != nil {
			return "", fmt.Errorf("workflow_run: %w (pass `workflow` or `steps`)", err)
		}
		runName = strings.TrimSpace(strString(in, "name"))
		if runName == "" {
			runName = "ad-hoc"
		}
	}

	run, err := t.store.StartRun(ctx, workflowID, runName, steps, input, "agent", strings.TrimSpace(strString(in, "depends_on")))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "run_id": run.ID, "workflow": runName, "steps": len(steps),
		"status":  string(run.Status),
		"message": fmt.Sprintf("Run %s started (%d steps). The engine advances it automatically - poll workflow_status.", run.ID, len(steps)),
	})
	return string(out), nil
}

// ── workflow_status ─────────────────────────────────────────────────────────

type workflowStatusTool struct{ store *workflow.Store }

func (t *workflowStatusTool) Name() string { return "workflow_status" }
func (t *workflowStatusTool) Description() string {
	return "Get the current state of a workflow run - overall status plus every " +
		"step's status, output, and error. Use the run id returned by workflow_run."
}
func (t *workflowStatusTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"run_id": map[string]any{"type": "string"}},
		"required":   []string{"run_id"},
	}
}
func (t *workflowStatusTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	runID := strings.TrimSpace(strString(in, "run_id"))
	if runID == "" {
		return "", errors.New("workflow_status: run_id is required")
	}
	run, err := t.store.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if run == nil {
		return "", fmt.Errorf("workflow_status: no run with id %s", runID)
	}
	b, _ := json.MarshalIndent(run, "", "  ")
	return string(b), nil
}

// ── workflow_resume ─────────────────────────────────────────────────────────

type workflowResumeTool struct{ store *workflow.Store }

func (t *workflowResumeTool) Name() string { return "workflow_resume" }
func (t *workflowResumeTool) Description() string {
	return "Resolve a workflow that's paused at a checkpoint. approve=true lets it " +
		"continue past the checkpoint; approve=false skips that step and continues. " +
		"Either way the run goes back to running and the engine picks it up."
}
func (t *workflowResumeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id":  map[string]any{"type": "string"},
			"approve": map[string]any{"type": "boolean", "description": "true = proceed past the checkpoint, false = skip it. Default true."},
		},
		"required": []string{"run_id"},
	}
}
func (t *workflowResumeTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	runID := strings.TrimSpace(strString(in, "run_id"))
	if runID == "" {
		return "", errors.New("workflow_resume: run_id is required")
	}
	approve := true
	if v, ok := in["approve"].(bool); ok {
		approve = v
	}
	run, err := t.store.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if run == nil {
		return "", fmt.Errorf("workflow_resume: no run with id %s", runID)
	}
	var awaiting *workflow.Step
	for i := range run.Steps {
		if run.Steps[i].Status == workflow.StepAwaiting {
			awaiting = &run.Steps[i]
			break
		}
	}
	if awaiting == nil {
		return "", fmt.Errorf("workflow_resume: run %s has no checkpoint awaiting", runID)
	}
	if _, err := t.store.ResolveCheckpoint(ctx, awaiting.ID, approve); err != nil {
		return "", err
	}
	if err := t.store.SetRunStatus(ctx, runID, workflow.RunRunning, ""); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "run_id": runID, "approved": approve,
		"message": "Checkpoint resolved - the run is back to running.",
	})
	return string(out), nil
}

// ── workflow_cancel ─────────────────────────────────────────────────────────

type workflowCancelTool struct{ store *workflow.Store }

func (t *workflowCancelTool) Name() string { return "workflow_cancel" }
func (t *workflowCancelTool) Description() string {
	return "Cancel a workflow run. The engine stops advancing it; completed steps " +
		"are kept for history."
}
func (t *workflowCancelTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"run_id": map[string]any{"type": "string"}},
		"required":   []string{"run_id"},
	}
}
func (t *workflowCancelTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	runID := strings.TrimSpace(strString(in, "run_id"))
	if runID == "" {
		return "", errors.New("workflow_cancel: run_id is required")
	}
	if err := t.store.SetRunStatus(ctx, runID, workflow.RunCancelled, "cancelled by boss"); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "run_id": runID, "status": "cancelled"})
	return string(out), nil
}

// ── workflow_validate ───────────────────────────────────────────────────────

type workflowValidateTool struct{ store *workflow.Store }

func (t *workflowValidateTool) Name() string { return "workflow_validate" }
func (t *workflowValidateTool) Description() string {
	return "Statically check a workflow's step list before you run it - kinds are " +
		"valid, each step's spec carries what its kind needs, and every " +
		"{{steps.N.output}} reference points at an earlier step. Pass `workflow` " +
		"(a saved name) or inline `steps`. Returns the problems found, or confirms " +
		"the assembly is well-formed. Cheap insurance - run it before workflow_run."
}
func (t *workflowValidateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workflow": map[string]any{"type": "string", "description": "Name of a saved workflow to validate. Omit if passing inline steps."},
			"steps":    stepsParamSchema(),
		},
	}
}
func (t *workflowValidateTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	var steps []workflow.StepDef
	if wfName := strings.TrimSpace(strString(in, "workflow")); wfName != "" {
		wf, err := t.store.GetWorkflow(ctx, wfName)
		if err != nil {
			return "", err
		}
		if wf == nil {
			return "", fmt.Errorf("workflow_validate: no saved workflow named %q", wfName)
		}
		steps = wf.Steps
	} else {
		parsed, err := parseSteps(in["steps"])
		if err != nil {
			return "", fmt.Errorf("workflow_validate: %w (pass `workflow` or `steps`)", err)
		}
		steps = parsed
	}
	problems := workflow.ValidateSteps(steps)
	out, _ := json.Marshal(map[string]any{
		"ok":       len(problems) == 0,
		"steps":    len(steps),
		"problems": problems,
		"message": map[bool]string{
			true:  "Well-formed - safe to run.",
			false: fmt.Sprintf("%d problem(s) found - fix before running.", len(problems)),
		}[len(problems) == 0],
	})
	return string(out), nil
}

// ── workflow_list ───────────────────────────────────────────────────────────

type workflowListTool struct{ store *workflow.Store }

func (t *workflowListTool) Name() string { return "workflow_list" }
func (t *workflowListTool) Description() string {
	return "List saved workflows and recent runs - names, step counts, and run " +
		"statuses. Use to see what workflows exist before creating a duplicate."
}
func (t *workflowListTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *workflowListTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	workflows, err := t.store.ListWorkflows(ctx)
	if err != nil {
		return "", err
	}
	runs, err := t.store.ListRuns(ctx, 25)
	if err != nil {
		return "", err
	}
	wfOut := make([]map[string]any, 0, len(workflows))
	for _, wf := range workflows {
		wfOut = append(wfOut, map[string]any{
			"name": wf.Name, "description": wf.Description, "steps": len(wf.Steps), "enabled": wf.Enabled,
		})
	}
	runOut := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		runOut = append(runOut, map[string]any{
			"run_id": r.ID, "workflow": r.WorkflowName, "status": string(r.Status),
			"current_step": r.CurrentStep, "created_at": r.CreatedAt,
		})
	}
	b, _ := json.MarshalIndent(map[string]any{"workflows": wfOut, "recent_runs": runOut}, "", "  ")
	return string(b), nil
}
