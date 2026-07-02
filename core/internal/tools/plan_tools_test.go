package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// nilStore is a plan.Store backed by a nil pool. Every read method returns
// (nil, nil) so we can exercise resolveStepRef logic without a real database.
var nilStore = plan.NewStore(nil)

// TestResolveStepRef_UUID verifies that a well-formed UUID passes through
// resolveStepRef untouched — no DB call, no error — even when there is no
// session in context and no plan in the store.
func TestResolveStepRef_UUID(t *testing.T) {
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	got, err := resolveStepRef(context.Background(), nilStore, uuid)
	if err != nil {
		t.Fatalf("unexpected error for UUID ref: %v", err)
	}
	if got != uuid {
		t.Fatalf("resolveStepRef UUID = %q, want %q", got, uuid)
	}
}

// TestResolveStepRef_Positional_NoActivePlan verifies that when there is no
// active plan at all (store has nil pool), a positional step reference like "5"
// returns an actionable error that tells the caller to create a plan — NOT the
// old misleading "call plan_get first" message. This proves the fallback path
// (GetAnyActive) runs and the error surface is clear.
func TestResolveStepRef_Positional_NoActivePlan(t *testing.T) {
	_, err := resolveStepRef(context.Background(), nilStore, "5")
	if err == nil {
		t.Fatal("expected an error for positional ref with no active plan, got nil")
	}
	// Must not tell the user to "call plan_get first" — that's the old
	// misleading message this fix removes.
	if strings.Contains(err.Error(), "plan_get") {
		t.Fatalf("error still mentions plan_get (old behaviour): %v", err)
	}
	// Should mention plan_create so the user knows what to do.
	if !strings.Contains(err.Error(), "plan_create") {
		t.Fatalf("error should mention plan_create, got: %v", err)
	}
}

// TestResolveStepRef_Positional_EmptyString verifies that an empty step_id is
// rejected cleanly rather than panicking or silently passing through.
func TestResolveStepRef_EmptyStepID(t *testing.T) {
	_, err := resolveStepRef(context.Background(), nilStore, "")
	if err == nil {
		t.Fatal("expected error for empty step_id, got nil")
	}
}

// TestResolveStepRef_MangledPositional verifies that a sloppy positional
// emission like "2'}}," (model hallucination) is recovered to integer 2 and
// then follows the no-active-plan path correctly (returns error, not panic).
func TestResolveStepRef_MangledPositional(t *testing.T) {
	_, err := resolveStepRef(context.Background(), nilStore, "2'}}," )
	if err == nil {
		t.Fatal("expected error for mangled positional with no active plan, got nil")
	}
	// Should not mention plan_get — same fix as the clean positional case.
	if strings.Contains(err.Error(), "plan_get") {
		t.Fatalf("error still mentions plan_get for mangled positional: %v", err)
	}
}

// TestPlanUpdate_ExecuteWithNoSession proves that plan_update.Execute returns a
// useful error (not a panic) when called with a positional step_id and no
// session context AND no active plan in the store. This is the exact scenario
// that previously surfaced the confusing "call plan_get first" message and the
// root bug this fix addresses.
func TestPlanUpdate_ExecuteWithNoSession(t *testing.T) {
	tool := &planUpdate{store: nilStore}
	_, err := tool.Execute(context.Background(), map[string]any{
		"step_id": "5",
		"status":  "done",
	})
	if err == nil {
		t.Fatal("expected error from plan_update with positional ref and no active plan")
	}
	// Must not reference plan_get.
	if strings.Contains(err.Error(), "plan_get") {
		t.Fatalf("plan_update still emits plan_get guidance (old behaviour): %v", err)
	}
}

// TestPlanUpdate_StaleStepIDNoPlan verifies that when plan_update is called
// with a UUID step id that no longer exists in the store (stale after plan
// recreate / session switch), and no active plan is available, it returns a
// descriptive error rather than panicking. When an active plan IS available
// the result would also carry it (exercised by integration tests with a real pool).
func TestPlanUpdate_StaleStepIDNoPlan(t *testing.T) {
	tool := &planUpdate{store: nilStore}
	out, err := tool.Execute(context.Background(), map[string]any{
		"step_id": "550e8400-e29b-41d4-a716-446655440000", // valid UUID, not in store
		"status":  "done",
	})
	// With a nil pool, GetActiveBySession + GetAnyActive both return nil, so
	// the tool falls back to the bare error (not a panic, not plan_get guidance).
	if err == nil && !strings.Contains(out, "550e8400") {
		t.Fatal("expected an error or an error JSON containing the step id")
	}
	// Must not reference plan_get in the error path.
	combined := out + err.Error()
	if strings.Contains(combined, "plan_get") {
		t.Fatalf("stale-step error still mentions plan_get: %s / %v", out, err)
	}
}

// stubPlanGetter is a minimal planGetter for tests: returns injected plans
// without hitting a real database. bySession is returned from
// GetActiveBySession; anyActive from GetAnyActive.
type stubPlanGetter struct {
	bySession *plan.Plan
	anyActive *plan.Plan
}

func (s *stubPlanGetter) Get(_ context.Context, _ string) (*plan.Plan, error) { return nil, nil }
func (s *stubPlanGetter) GetActiveBySession(_ context.Context, _ string) (*plan.Plan, error) {
	return s.bySession, nil
}
func (s *stubPlanGetter) GetAnyActive(_ context.Context) (*plan.Plan, error) {
	return s.anyActive, nil
}

// TestPlanGet_FallsBackToGlobalWhenAutonomous: for AUTONOMOUS turns (cron,
// heartbeat, resumed background runs) that mint a fresh session UUID per run,
// plan_get must still fall back to GetAnyActive so the run can reach the plan it
// is executing. This preserves the 2026-06-14 resume behavior — but ONLY when
// autonomous.
func TestPlanGet_FallsBackToGlobalWhenAutonomous(t *testing.T) {
	activePlan := &plan.Plan{ID: "plan-abc", Status: plan.PlanActive, Steps: []plan.Step{}}
	stub := &stubPlanGetter{bySession: nil, anyActive: activePlan}
	tool := &planGet{store: stub}
	out, err := tool.Execute(WithAutonomous(context.Background()), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "plan-abc") {
		t.Fatalf("autonomous plan_get should return the cross-session active plan, got: %s", out)
	}
}

// TestPlanGet_NoCrossSessionBleedWhenInteractive is the 2026-07-02 regression
// guard. An INTERACTIVE chat turn (not marked autonomous) whose own session has
// no plan MUST NOT inherit another session's active plan — that bleed made a
// fresh "make me a report" session grab a stranger's plan c03495f5 and mutate
// it. plan_get returns null so the model correctly plan_creates its own.
func TestPlanGet_NoCrossSessionBleedWhenInteractive(t *testing.T) {
	otherPlan := &plan.Plan{ID: "plan-stranger", Status: plan.PlanActive, Steps: []plan.Step{}}
	stub := &stubPlanGetter{bySession: nil, anyActive: otherPlan}
	tool := &planGet{store: stub}
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "plan-stranger") {
		t.Fatalf("interactive plan_get must NOT bleed a stranger's plan, got: %s", out)
	}
	if !strings.Contains(out, "null") {
		t.Fatalf("interactive plan_get with no own plan should return null, got: %s", out)
	}
}

// TestPlanGet_ReturnsNullWhenNoPlanAnywhere verifies that plan_get returns
// {"plan":null} (not an error) when neither session nor global lookup finds a
// plan — the caller should get a clear null signal, not a crash.
func TestPlanGet_ReturnsNullWhenNoPlanAnywhere(t *testing.T) {
	stub := &stubPlanGetter{bySession: nil, anyActive: nil}
	tool := &planGet{store: stub}
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected no error when no plan exists, got: %v", err)
	}
	if !strings.Contains(out, "null") {
		t.Fatalf("expected null plan in output, got: %s", out)
	}
}
