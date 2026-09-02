package main

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// nestedPlanSink routes a nested Claude Code job's own TodoWrite checklist
// onto mem_plans, through the SAME store seam `todo_write` writes.
//
// It is a thin adapter on purpose: the ownership rule (a nested job may only
// replace steps on a plan it authored) and the settle rule both live in
// plan.Store, where they are enforced in the same transaction as the write,
// rather than in whichever caller remembered them.
type nestedPlanSink struct{ plans *plan.Store }

func (s nestedPlanSink) Sync(ctx context.Context, c tools.NestedChecklist) error {
	_, err := s.plans.SyncNestedChecklist(ctx, c.SessionID, c.RunID, c.Title, c.Items)
	return err
}

func (s nestedPlanSink) Settle(ctx context.Context, runID string, failed bool, summary string) error {
	status := plan.StepDone
	if failed {
		status = plan.StepFailed
	}
	_, err := s.plans.SettleOwnedPlan(ctx, runID, status, summary)
	return err
}
