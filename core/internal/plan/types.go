// Package plan is the durable PLAN substrate ("the Cortex") behind
// plan -> execute -> verify -> replan.
//
// A Plan is the agent's own working memory of a multi-step task, made durable
// in Postgres so it survives compaction, restart, and session boundaries. The
// AGENT executes the steps in its own loop; this package is plain CRUD over
// mem_plans / mem_plan_steps, NOT an autonomous execution engine (that is
// workflow.Engine). The plan_create / plan_update / plan_verify native tools
// are the contract the LLM assembles against; PlanProvider re-injects the
// active plan every turn so the agent always sees where it left off.
package plan

import "time"

// Plan statuses.
const (
	// PlanProposed: laid out for the boss but NOT approved. Not executable,
	// not in the chat dock, never adopted by a later session. plan_approve
	// (or Studio's "Go ahead") makes it active and stamps approved_at.
	PlanProposed  = "proposed"
	PlanActive    = "active"
	PlanPaused    = "paused"
	PlanCompleted = "completed"
	PlanFailed    = "failed"
	PlanCancelled = "cancelled"
)

// Step statuses.
const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepBlocked    = "blocked" // verification failed / awaiting replan
	StepDone       = "done"
	StepFailed     = "failed"
	StepSkipped    = "skipped"
)

// Plan is one durable, session-owned multi-step task.
type Plan struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id,omitempty"`
	GoalID      string    `json:"goal_id,omitempty"`
	Title       string    `json:"title"`
	Goal        string    `json:"goal,omitempty"`
	Status      string    `json:"status"`
	CurrentStep int       `json:"current_step"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// ApprovedAt is when the boss (or an autonomous work order) approved
	// the plan for execution. nil on a proposal.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	Steps      []Step     `json:"steps,omitempty"`
}

// Approved reports whether the plan may be executed / resumed.
func (p *Plan) Approved() bool { return p != nil && p.Status != PlanProposed && p.ApprovedAt != nil }

// Step is one ordered item in a plan's checklist.
type Step struct {
	ID             string         `json:"id"`
	PlanID         string         `json:"plan_id"`
	Idx            int            `json:"idx"`
	Title          string         `json:"title"`
	Detail         string         `json:"detail,omitempty"`
	Status         string         `json:"status"`
	IsCheckpoint   bool           `json:"is_checkpoint"`
	VerifyRequired bool           `json:"verify_required"`
	VerifyResult   map[string]any `json:"verify_result,omitempty"`
	ResultSummary  string         `json:"result_summary,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	// RecoveryAttempted guards the one-shot "auto-recovery, then stop" policy:
	// a transient/bridge-class background failure may be re-dispatched once;
	// after that this is true and a second failure stops + surfaces.
	RecoveryAttempted bool       `json:"recovery_attempted,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
}

// NewStepInput is the shape plan_create accepts for each step it lays out.
type NewStepInput struct {
	Title          string `json:"title"`
	Detail         string `json:"detail"`
	IsCheckpoint   bool   `json:"is_checkpoint"`
	VerifyRequired bool   `json:"verify_required"`
}

// NormalizeStepStatus maps loose model input to a canonical step status.
func NormalizeStepStatus(s string) string {
	switch s {
	case StepInProgress, "in-progress", "active", "doing", "started":
		return StepInProgress
	case StepDone, "complete", "completed", "finished":
		return StepDone
	case StepBlocked, "stuck":
		return StepBlocked
	case StepFailed, "error":
		return StepFailed
	case StepSkipped, "skip":
		return StepSkipped
	default:
		return StepPending
	}
}
