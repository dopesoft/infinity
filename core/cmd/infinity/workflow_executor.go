package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/eval"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/workflow"
)

// workflowExecutor is the concrete workflow.Executor. It dispatches a
// workflow step to the right subsystem - a native/MCP tool, a skill, or a
// sub-agent turn. It lives in package main so it can depend on every
// subsystem; the workflow package itself stays dependency-light behind the
// workflow.Executor interface (same pattern as cron's executors).
type workflowExecutor struct {
	registry    *tools.Registry
	skillRunner *skills.Runner
	loop        *agent.Loop
}

func (e *workflowExecutor) Execute(ctx context.Context, step workflow.Step, _ map[string]any) (string, error) {
	switch step.Kind {
	case workflow.KindTool:
		return e.runTool(ctx, step)
	case workflow.KindSkill:
		return e.runSkill(ctx, step)
	case workflow.KindAgent:
		return e.runAgent(ctx, step)
	default:
		return "", fmt.Errorf("workflow executor: unsupported step kind %q", step.Kind)
	}
}

// runTool invokes a registered native/MCP tool with the step's args.
func (e *workflowExecutor) runTool(ctx context.Context, step workflow.Step) (string, error) {
	name, _ := step.Spec["tool"].(string)
	if name == "" {
		return "", errors.New("tool step: spec.tool is required")
	}
	if e.registry == nil {
		return "", errors.New("tool step: no tool registry wired")
	}
	tool, ok := e.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("tool step: unknown tool %q", name)
	}
	args, _ := step.Spec["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	return tool.Execute(ctx, args)
}

// runSkill invokes a skill through the skill runner.
func (e *workflowExecutor) runSkill(ctx context.Context, step workflow.Step) (string, error) {
	name, _ := step.Spec["skill"].(string)
	if name == "" {
		return "", errors.New("skill step: spec.skill is required")
	}
	if e.skillRunner == nil {
		return "", errors.New("skill step: no skill runner wired")
	}
	args, _ := step.Spec["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	res, _, err := e.skillRunner.Invoke(ctx, "", name, args, "workflow")
	if err != nil {
		if res.Stderr != "" {
			return "", fmt.Errorf("%w (stderr: %s)", err, res.Stderr)
		}
		return "", err
	}
	return res.Stdout, nil
}

// runAgent runs a sub-agent turn in a fresh isolated session and returns
// the accumulated text response. Safe whether or not Loop.Run closes the
// event channel: we select on a separate result channel.
func (e *workflowExecutor) runAgent(ctx context.Context, step workflow.Step) (string, error) {
	prompt, _ := step.Spec["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("agent step: spec.prompt is required")
	}
	if e.loop == nil {
		return "", errors.New("agent step: no agent loop wired")
	}
	sessionID := uuid.NewString()
	out := make(chan agent.RunEvent, 128)
	errCh := make(chan error, 1)
	go func() { errCh <- e.loop.Run(ctx, sessionID, prompt, "", nil, out) }()

	var (
		sb     strings.Builder
		runErr string
	)
	collect := func(ev agent.RunEvent) {
		switch ev.Kind {
		case agent.EventDelta:
			sb.WriteString(ev.TextDelta)
		case agent.EventError:
			runErr = ev.Error
		}
	}
	for {
		select {
		case ev := <-out:
			collect(ev)
		case err := <-errCh:
			// Run returned - drain whatever is still buffered, non-blocking.
			for drained := false; !drained; {
				select {
				case ev := <-out:
					collect(ev)
				default:
					drained = true
				}
			}
			if err != nil {
				return "", err
			}
			if runErr != "" {
				return "", errors.New(runErr)
			}
			return strings.TrimSpace(sb.String()), nil
		}
	}
}

// checkpointSurfacer puts a paused-workflow checkpoint in front of the boss
// as a generic surface item - a workflow waiting on approval shows up on
// the dashboard without the boss digging into the workflow tab.
type checkpointSurfacer struct {
	store *surface.Store
}

func (c *checkpointSurfacer) SurfaceCheckpoint(ctx context.Context, run *workflow.Run, step workflow.Step, message string) error {
	if c == nil || c.store == nil {
		return nil
	}
	title := strings.TrimSpace(message)
	if title == "" {
		title = fmt.Sprintf("Workflow %q is waiting on you", run.WorkflowName)
	}
	importance := 85
	_, err := c.store.Upsert(ctx, &surface.Item{
		Surface:    "approvals",
		Kind:       "checkpoint",
		Source:     "workflow",
		ExternalID: "workflow-checkpoint-" + step.ID,
		Title:      title,
		Subtitle:   fmt.Sprintf("%s · step %d", run.WorkflowName, step.StepIndex),
		Body:       message,
		Metadata: map[string]any{
			"run_id":      run.ID,
			"step_id":     step.ID,
			"step_index":  step.StepIndex,
			"resume_with": "workflow_resume",
		},
		Importance:       &importance,
		ImportanceReason: "Workflow paused - needs your approval to continue",
	})
	return err
}

// workflowEvalRecorder records a finished workflow run's outcome to the
// verification substrate (mem_evals), so workflow success rates and
// regressions show up on the same scorecards as skills and tools.
type workflowEvalRecorder struct {
	store *eval.Store
}

func (r *workflowEvalRecorder) RecordRun(ctx context.Context, run *workflow.Run, outcome, note string) {
	if r == nil || r.store == nil {
		return
	}
	o := eval.OutcomeSuccess
	if outcome == "failure" {
		o = eval.OutcomeFailure
	}
	_ = r.store.Record(ctx, &eval.Eval{
		SubjectKind: "workflow",
		SubjectName: run.WorkflowName,
		RunID:       run.ID,
		Outcome:     o,
		Notes:       note,
		Source:      "engine",
	})
}
