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

// TestPlanGet_FallsBackToGlobalWhenSessionMisses is the regression test for the
// 2026-06-14 curiosity item. When plan_get is called in a new session (after
// context loss), GetActiveBySession returns nil because the plan lives under the
// old session ID. plan_get must fall back to GetAnyActive and return that plan,
// not null — so the agent can resume a multi-step task without a spurious error.
func TestPlanGet_FallsBackToGlobalWhenSessionMisses(t *testing.T) {
	activePlan := &plan.Plan{ID: "plan-abc", Status: plan.PlanActive, Steps: []plan.Step{}}
	stub := &stubPlanGetter{bySession: nil, anyActive: activePlan}
	tool := &planGet{store: stub}
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "plan-abc") {
		t.Fatalf("plan_get should return the cross-session active plan, got: %s", out)
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
