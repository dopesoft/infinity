// Package workflow implements durable, resumable multi-step workflows —
// Phase 2 of the assembly substrate.
//
// A workflow is a first-class object. The agent assembles one from natural
// language (a step list); the Engine runs it as a state machine that
// persists after every step, so a process restart resumes mid-workflow.
//
// Skills are single recipes. Workflows chain them — plus tools, sub-agent
// turns, and human checkpoints — into processes that run over hours or
// days. The agent never writes the tables with raw SQL; the workflow_*
// native tools are the contract.
package workflow

import (
	"context"
	"time"
)

// RunStatus is the lifecycle of a workflow run.
type RunStatus string

const (
	RunPending   RunStatus = "pending"   // claimed by the engine next tick
	RunRunning   RunStatus = "running"   // actively advancing, step by step
	RunPaused    RunStatus = "paused"    // blocked on a checkpoint
	RunDone      RunStatus = "done"      // every step finished
	RunFailed    RunStatus = "failed"    // a step exhausted its retries
	RunCancelled RunStatus = "cancelled" // stopped by the boss
)

// StepStatus is the lifecycle of one step within a run.
type StepStatus string

const (
	StepPending  StepStatus = "pending"
	StepRunning  StepStatus = "running"
	StepDone     StepStatus = "done"
	StepFailed   StepStatus = "failed"
	StepSkipped  StepStatus = "skipped"
	StepAwaiting StepStatus = "awaiting" // checkpoint — blocked on the boss
)

// StepKind is how a step executes.
type StepKind string

const (
	KindTool       StepKind = "tool"       // invoke a native / MCP tool
	KindSkill      StepKind = "skill"      // invoke a skill
	KindAgent      StepKind = "agent"      // run a sub-agent turn with a prompt
	KindCheckpoint StepKind = "checkpoint" // pause for boss approval
)

func (k StepKind) Valid() bool {
	switch k {
	case KindTool, KindSkill, KindAgent, KindCheckpoint:
		return true
	}
	return false
}

// StepDef is one step in a workflow definition. Spec shape depends on Kind:
//
//	tool:       { "tool": "surface_item", "args": { … } }
//	skill:      { "skill": "inbox-triage", "args": { … } }
//	agent:      { "prompt": "…" }
//	checkpoint: { "message": "Review before I proceed" }
//
// String values in Spec may reference prior state with {{input.KEY}} or
// {{steps.N.output}} — the engine resolves these before execution.
type StepDef struct {
	Name        string         `json:"name"`
	Kind        StepKind       `json:"kind"`
	Spec        map[string]any `json:"spec"`
	MaxAttempts int            `json:"max_attempts,omitempty"`
}

// Workflow is a saved, reusable definition.
type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Steps       []StepDef `json:"steps"`
	Source      string    `json:"source"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Run is one execution instance — the durable thing.
type Run struct {
	ID            string         `json:"id"`
	WorkflowID    string         `json:"workflowId,omitempty"`
	WorkflowName  string         `json:"workflowName"`
	Status        RunStatus      `json:"status"`
	TriggerSource string         `json:"triggerSource"`
	Input         map[string]any `json:"input"`
	Context       map[string]any `json:"context"`
	CurrentStep   int            `json:"currentStep"`
	Error         string         `json:"error,omitempty"`
	StartedAt     *time.Time     `json:"startedAt,omitempty"`
	FinishedAt    *time.Time     `json:"finishedAt,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	Steps         []Step         `json:"steps,omitempty"`
}

// Step is per-run step state.
type Step struct {
	ID          string         `json:"id"`
	RunID       string         `json:"runId"`
	StepIndex   int            `json:"stepIndex"`
	Name        string         `json:"name"`
	Kind        StepKind       `json:"kind"`
	Spec        map[string]any `json:"spec"`
	Status      StepStatus     `json:"status"`
	Attempt     int            `json:"attempt"`
	MaxAttempts int            `json:"maxAttempts"`
	Output      string         `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
	StartedAt   *time.Time     `json:"startedAt,omitempty"`
	FinishedAt  *time.Time     `json:"finishedAt,omitempty"`
}

// Executor runs one non-checkpoint step. serve.go provides the concrete
// implementation that dispatches to the tools.Registry / skill Runner /
// agent loop — the workflow package stays dependency-light behind this
// interface (same pattern as cron's CronScheduler).
type Executor interface {
	Execute(ctx context.Context, step Step, runInput map[string]any) (output string, err error)
}
