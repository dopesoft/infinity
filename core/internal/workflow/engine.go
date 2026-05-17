package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Engine is the durable workflow runner. A single background worker polls
// for runnable runs, advances each one exactly one step per tick via the
// injected Executor, and persists state after every step - so a process
// restart resumes mid-workflow exactly where it left off.
type Engine struct {
	store        *Store
	executor     Executor
	logger       *slog.Logger
	interval     time.Duration
	surfacer     CheckpointSurfacer
	evalRecorder EvalRecorder
}

// CheckpointSurfacer lets the engine put a checkpoint in front of the boss
// without the workflow package importing the surface package. serve.go
// provides the implementation (it writes a mem_surface_items row).
type CheckpointSurfacer interface {
	SurfaceCheckpoint(ctx context.Context, run *Run, step Step, message string) error
}

// EvalRecorder lets the engine record a run's outcome to the verification
// substrate without the workflow package importing the eval package.
// serve.go provides the implementation. outcome is "success" or "failure".
type EvalRecorder interface {
	RecordRun(ctx context.Context, run *Run, outcome, note string)
}

func NewEngine(store *Store, executor Executor, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:    store,
		executor: executor,
		logger:   logger,
		interval: 3 * time.Second,
	}
}

// WithCheckpointSurfacer wires the optional checkpoint surfacer.
func (e *Engine) WithCheckpointSurfacer(s CheckpointSurfacer) *Engine {
	e.surfacer = s
	return e
}

// WithEvalRecorder wires the optional eval recorder - every run that
// reaches a terminal state then records a success/failure outcome to the
// verification substrate.
func (e *Engine) WithEvalRecorder(r EvalRecorder) *Engine {
	e.evalRecorder = r
	return e
}

// finishRun moves a run to a terminal status and records the outcome.
func (e *Engine) finishRun(ctx context.Context, run *Run, status RunStatus, note string) {
	_ = e.store.SetRunStatus(ctx, run.ID, status, note)
	if e.evalRecorder != nil {
		outcome := "success"
		if status == RunFailed {
			outcome = "failure"
		}
		e.evalRecorder.RecordRun(ctx, run, outcome, note)
	}
}

// Start launches the background worker. Non-blocking. On boot it reclaims
// orphaned steps left 'running' by a process that died.
func (e *Engine) Start(ctx context.Context) {
	if e == nil || e.store == nil || e.executor == nil {
		return
	}
	if n, err := e.store.ReclaimOrphans(ctx); err != nil {
		e.logger.Error("workflow: reclaim orphans", "err", err)
	} else if n > 0 {
		e.logger.Info("workflow: reclaimed orphaned steps", "count", n)
	}
	go e.loop(ctx)
}

func (e *Engine) loop(ctx context.Context) {
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

// tick claims one runnable run and advances it exactly one step. One step
// per tick keeps progress fair across concurrent runs and bounds the work
// done while holding any single run.
func (e *Engine) tick(ctx context.Context) {
	run, err := e.store.ClaimRunnable(ctx)
	if err != nil {
		e.logger.Error("workflow: claim", "err", err)
		return
	}
	if run == nil {
		return
	}
	e.advance(ctx, run)
}

// advance executes the next actionable step of a run.
func (e *Engine) advance(ctx context.Context, run *Run) {
	step := nextActionable(run)
	if step == nil {
		// Every step is terminal - the run is finished.
		if anyFailed(run) {
			e.finishRun(ctx, run, RunFailed, "a step failed")
		} else {
			e.finishRun(ctx, run, RunDone, "")
			e.logger.Info("workflow: run complete", "run", run.ID, "workflow", run.WorkflowName)
		}
		return
	}

	// A checkpoint already awaiting → the run stays paused; nothing to do
	// until the boss resolves it via workflow_resume.
	if step.Status == StepAwaiting {
		_ = e.store.SetRunStatus(ctx, run.ID, RunPaused, "")
		return
	}

	// Checkpoint reached for the first time → pause + surface it.
	if step.Kind == KindCheckpoint {
		if err := e.store.AwaitStep(ctx, step.ID); err != nil {
			e.logger.Error("workflow: await checkpoint", "err", err, "run", run.ID)
			return
		}
		_ = e.store.SetRunStatus(ctx, run.ID, RunPaused, "")
		if e.surfacer != nil {
			msg, _ := step.Spec["message"].(string)
			if err := e.surfacer.SurfaceCheckpoint(ctx, run, *step, msg); err != nil {
				e.logger.Error("workflow: surface checkpoint", "err", err, "run", run.ID)
			}
		}
		e.logger.Info("workflow: paused at checkpoint", "run", run.ID, "step", step.StepIndex)
		return
	}

	// Executable step (tool / skill / agent).
	if err := e.store.StartStep(ctx, step.ID); err != nil {
		e.logger.Error("workflow: start step", "err", err, "run", run.ID)
		return
	}
	step.Attempt++ // mirror the DB increment StartStep just made

	resolved := *step
	resolved.Spec = resolveSpec(step.Spec, run)

	output, execErr := e.executor.Execute(ctx, resolved, run.Input)
	if execErr != nil {
		retried, ferr := e.store.FailStep(ctx, step, execErr.Error())
		if ferr != nil {
			e.logger.Error("workflow: fail step", "err", ferr, "run", run.ID)
			return
		}
		if retried {
			e.logger.Info("workflow: step failed, will retry",
				"run", run.ID, "step", step.StepIndex, "attempt", step.Attempt, "err", execErr)
			return
		}
		e.finishRun(ctx, run, RunFailed,
			fmt.Sprintf("step %d (%s) failed: %v", step.StepIndex, step.Name, execErr))
		e.logger.Error("workflow: run failed", "run", run.ID, "step", step.StepIndex, "err", execErr)
		return
	}

	if err := e.store.CompleteStep(ctx, step.ID, output); err != nil {
		e.logger.Error("workflow: complete step", "err", err, "run", run.ID)
		return
	}
	if err := e.store.AdvanceRun(ctx, run.ID, step.StepIndex+1, step.StepIndex, output); err != nil {
		e.logger.Error("workflow: advance run", "err", err, "run", run.ID)
		return
	}
	if step.StepIndex == len(run.Steps)-1 {
		e.finishRun(ctx, run, RunDone, "")
		e.logger.Info("workflow: run complete", "run", run.ID, "workflow", run.WorkflowName)
	}
}

// nextActionable returns the first non-terminal step - pending, awaiting,
// or (after a crash) running. Returns nil when every step is terminal.
func nextActionable(run *Run) *Step {
	for i := range run.Steps {
		switch run.Steps[i].Status {
		case StepPending, StepAwaiting, StepRunning:
			return &run.Steps[i]
		}
	}
	return nil
}

func anyFailed(run *Run) bool {
	for i := range run.Steps {
		if run.Steps[i].Status == StepFailed {
			return true
		}
	}
	return false
}

// resolveSpec walks a step spec and substitutes {{input.KEY}} and
// {{steps.N.output}} references in string values with prior run state.
// This is what lets the agent chain steps - "feed step 0's output into
// step 1" - without the engine knowing anything domain-specific.
func resolveSpec(spec map[string]any, run *Run) map[string]any {
	if len(spec) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(spec))
	for k, v := range spec {
		out[k] = resolveValue(v, run)
	}
	return out
}

func resolveValue(v any, run *Run) any {
	switch x := v.(type) {
	case string:
		return resolveString(x, run)
	case map[string]any:
		return resolveSpec(x, run)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = resolveValue(item, run)
		}
		return out
	default:
		return v
	}
}

func resolveString(s string, run *Run) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	if stepsCtx, ok := run.Context["steps"].(map[string]any); ok {
		for idx, out := range stepsCtx {
			if str, ok := out.(string); ok {
				s = strings.ReplaceAll(s, "{{steps."+idx+".output}}", str)
			}
		}
	}
	for k, val := range run.Input {
		s = strings.ReplaceAll(s, "{{input."+k+"}}", fmt.Sprintf("%v", val))
	}
	return s
}
