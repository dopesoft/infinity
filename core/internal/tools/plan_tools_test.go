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
func (s *stubPlanGetter) AdoptSession(_ context.Context, _, _ string) error { return nil }

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

// adoptingPlanGetter records AdoptSession calls so the tests below can assert
// that a cross-session pickup actually re-stamps ownership.
type adoptingPlanGetter struct {
	stubPlanGetter
	byID           *plan.Plan // returned from Get when IDs match
	adoptedPlan    string
	adoptedSession string
}

func (s *adoptingPlanGetter) Get(_ context.Context, id string) (*plan.Plan, error) {
	if s.byID != nil && s.byID.ID == id {
		return s.byID, nil
	}
	return nil, nil
}
func (s *adoptingPlanGetter) AdoptSession(_ context.Context, planID, sessionID string) error {
	s.adoptedPlan, s.adoptedSession = planID, sessionID
	return nil
}

// smartResumePlanGetter simulates AdoptSession by updating its internal session
// map so subsequent GetActiveBySession calls reflect the adoption — exactly
// what the real Store does when it UPDATEs session_id in Postgres. Used to
// test the full cross-session plan-resume flow without a real database.
type smartResumePlanGetter struct {
	plansByID      map[string]*plan.Plan
	plansBySession map[string]*plan.Plan
	anyActive      *plan.Plan
	adoptedPlan    string
	adoptedSession string
}

func (s *smartResumePlanGetter) Get(_ context.Context, id string) (*plan.Plan, error) {
	return s.plansByID[id], nil
}
func (s *smartResumePlanGetter) GetActiveBySession(_ context.Context, sid string) (*plan.Plan, error) {
	return s.plansBySession[sid], nil
}
func (s *smartResumePlanGetter) GetAnyActive(_ context.Context) (*plan.Plan, error) {
	return s.anyActive, nil
}
func (s *smartResumePlanGetter) AdoptSession(_ context.Context, planID, sessionID string) error {
	s.adoptedPlan, s.adoptedSession = planID, sessionID
	p := s.plansByID[planID]
	if p == nil {
		return nil
	}
	// Mirror the DB UPDATE: move plan from its old session to the new one.
	if old := p.SessionID; old != "" {
		delete(s.plansBySession, old)
	}
	p.SessionID = sessionID
	s.plansBySession[sessionID] = p
	return nil
}

// TestPlanPickup_AdoptsPlanOntoDrivingSession is the 2026-07-27 regression
// guard, and it is the defect that actually cost the boss a correct status.
//
// A cron RETRY mints a brand-new session per attempt. Attempt 2 reaches attempt
// 1's plan through this cross-session fallback and finishes the work — but the
// plan still pointed at attempt 1's dead session. Everything downstream is keyed
// by session, so all of it silently no-op'd: the cron finalize settled zero
// steps, the outcome classifier's incomplete-plan backstop looked up the live
// session, found no plan, and stamped "did_work". Result: the weekly AI digest
// was delivered, the run went green, and the kanban card spun on "Running"
// indefinitely.
//
// Picking a plan up must therefore mean owning it.
func TestPlanPickup_AdoptsPlanOntoDrivingSession(t *testing.T) {
	activePlan := &plan.Plan{ID: "plan-from-attempt-1", Status: plan.PlanActive}
	stub := &adoptingPlanGetter{stubPlanGetter: stubPlanGetter{bySession: nil, anyActive: activePlan}}

	ctx := WithSessionID(WithAutonomous(context.Background()), "attempt-2-session")
	got, err := anyActivePlanForTurn(ctx, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != "plan-from-attempt-1" {
		t.Fatalf("autonomous turn must reach the prior attempt's plan, got %+v", got)
	}
	if stub.adoptedPlan != "plan-from-attempt-1" {
		t.Fatalf("picked-up plan was not adopted; session-keyed finalize/classify will silently no-op (adopted=%q)", stub.adoptedPlan)
	}
	if stub.adoptedSession != "attempt-2-session" {
		t.Fatalf("plan adopted onto %q, want the session actually driving it", stub.adoptedSession)
	}
}

// TestPlanPickup_InteractiveNeverAdopts keeps the 2026-07-02 fix intact: an
// interactive chat turn must not reach — let alone take ownership of — another
// session's plan. Adoption makes that bleed strictly worse, so the autonomous
// gate has to come first.
func TestPlanPickup_InteractiveNeverAdopts(t *testing.T) {
	stranger := &plan.Plan{ID: "plan-stranger", Status: plan.PlanActive}
	stub := &adoptingPlanGetter{stubPlanGetter: stubPlanGetter{bySession: nil, anyActive: stranger}}

	got, err := anyActivePlanForTurn(WithSessionID(context.Background(), "chat-session"), stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("interactive turn must not pick up a stranger's plan, got %+v", got)
	}
	if stub.adoptedPlan != "" {
		t.Fatalf("interactive turn must never adopt a plan, adopted %q", stub.adoptedPlan)
	}
}

// ── Cross-session explicit plan resume regression (2026-08-26) ───────────────
//
// Bug: plan_get(plan_id=X) returned the plan but left its session_id pointing at
// the original session. Subsequent plan_update(step_id="2") called
// resolvePositionalStep → GetActiveBySession(currentSID) → nil, then fell
// through to anyActivePlanForTurn which is gated to autonomous-only → nil, and
// threw "there's no active plan to resolve step 2 against".
//
// Fix: plan_get with an explicit plan_id now calls AdoptSession when the plan
// belongs to a different session, re-anchoring it before the caller proceeds.

// TestPlanGet_ExplicitPlanID_ReAnchorsToCurrentSession verifies that plan_get
// called with an explicit plan_id for a plan from a DIFFERENT session calls
// AdoptSession, re-anchoring the plan onto the current session so that
// subsequent plan_update/plan_verify positional step refs can resolve.
func TestPlanGet_ExplicitPlanID_ReAnchorsToCurrentSession(t *testing.T) {
	oldSID := "83dce231-bc6a-4e69-81e0-d6c452d3daa4"
	newSID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	foreignPlan := &plan.Plan{
		ID:        "5145c6e7-daa0-4b93-ad0b-65e995b3fad4",
		SessionID: oldSID,
		Status:    plan.PlanPaused,
		Steps: []plan.Step{
			{ID: "step-1-uuid", Title: "Step 1"},
			{ID: "step-2-uuid", Title: "Step 2"},
		},
	}
	stub := &adoptingPlanGetter{byID: foreignPlan}
	tool := &planGet{store: stub}

	ctx := WithSessionID(context.Background(), newSID)
	out, err := tool.Execute(ctx, map[string]any{"plan_id": foreignPlan.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, foreignPlan.ID) {
		t.Fatalf("plan_get should return the requested plan, got: %s", out)
	}
	if stub.adoptedPlan != foreignPlan.ID {
		t.Fatalf("plan_get did not call AdoptSession; subsequent positional step refs will fail (adoptedPlan=%q)", stub.adoptedPlan)
	}
	if stub.adoptedSession != newSID {
		t.Fatalf("plan_get adopted onto wrong session: got %q, want %q", stub.adoptedSession, newSID)
	}
}

// TestPlanGet_ExplicitPlanID_AlreadyOwnedNoAdopt verifies that when plan_get is
// called with a plan_id that already belongs to the CURRENT session, AdoptSession
// is NOT called (no unnecessary DB roundtrip; the plan is already anchored).
func TestPlanGet_ExplicitPlanID_AlreadyOwnedNoAdopt(t *testing.T) {
	currentSID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ownPlan := &plan.Plan{
		ID:        "plan-already-mine",
		SessionID: currentSID,
		Status:    plan.PlanActive,
		Steps:     []plan.Step{{ID: "step-1-uuid", Title: "Step 1"}},
	}
	stub := &adoptingPlanGetter{byID: ownPlan}
	tool := &planGet{store: stub}

	ctx := WithSessionID(context.Background(), currentSID)
	out, err := tool.Execute(ctx, map[string]any{"plan_id": ownPlan.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, ownPlan.ID) {
		t.Fatalf("plan_get should return the plan, got: %s", out)
	}
	if stub.adoptedPlan != "" {
		t.Fatalf("plan_get must NOT call AdoptSession for a plan already owned by this session (adoptedPlan=%q)", stub.adoptedPlan)
	}
}

// TestPlanGet_ExplicitPlanID_NoSessionInContext verifies that when there is no
// session ID in context (CLI / test / unauthenticated call), plan_get with an
// explicit plan_id still returns the plan without calling AdoptSession — there
// is nothing to anchor to, so skipping is correct.
func TestPlanGet_ExplicitPlanID_NoSessionInContext(t *testing.T) {
	foreignPlan := &plan.Plan{
		ID:        "plan-foreign",
		SessionID: "some-other-session",
		Status:    plan.PlanPaused,
		Steps:     []plan.Step{{ID: "step-1-uuid", Title: "Step 1"}},
	}
	stub := &adoptingPlanGetter{byID: foreignPlan}
	tool := &planGet{store: stub}

	// No WithSessionID wrapping — SessionIDFromContext returns "".
	out, err := tool.Execute(context.Background(), map[string]any{"plan_id": foreignPlan.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, foreignPlan.ID) {
		t.Fatalf("plan_get should return the plan even without session context, got: %s", out)
	}
	if stub.adoptedPlan != "" {
		t.Fatalf("plan_get must not call AdoptSession when there is no session in context (adoptedPlan=%q)", stub.adoptedPlan)
	}
}

// TestCrossSessionResumeEndToEnd is the full regression guard for the durable
// plan resume bug: plan_get(plan_id) must re-anchor the plan so that
// GetActiveBySession for the NEW session finds it — which is exactly what
// resolvePositionalStep calls when plan_update(step_id="2") is invoked.
//
// This test uses smartResumePlanGetter, which mirrors the DB-level AdoptSession
// UPDATE by moving the plan between its internal session maps. It proves the
// complete contract:
//
//  1. plan_get(plan_id=X) calls AdoptSession(X, newSession).
//  2. After adoption GetActiveBySession(newSession) returns the plan.
//  3. Positional step refs resolve: Steps[1].ID is "step-2-uuid".
//
// Point 3 is the actual value resolvePositionalStep(ctx, store, 2) reads when
// the store is backed by a real DB (exercised by the DB-level SQL, not
// duplicated here with a mock pool).
func TestCrossSessionResumeEndToEnd(t *testing.T) {
	oldSID := "83dce231-bc6a-4e69-81e0-d6c452d3daa4"
	newSID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	resumedPlan := &plan.Plan{
		ID:        "5145c6e7-daa0-4b93-ad0b-65e995b3fad4",
		SessionID: oldSID,
		Status:    plan.PlanPaused,
		Steps: []plan.Step{
			{ID: "step-1-uuid", Title: "Step 1"},
			{ID: "step-2-uuid", Title: "Step 2"},
		},
	}
	stub := &smartResumePlanGetter{
		plansByID:      map[string]*plan.Plan{resumedPlan.ID: resumedPlan},
		plansBySession: map[string]*plan.Plan{oldSID: resumedPlan},
	}

	// Step 1: plan_get with explicit plan_id — must re-anchor.
	getPlan := &planGet{store: stub}
	ctx := WithSessionID(context.Background(), newSID)
	out, err := getPlan.Execute(ctx, map[string]any{"plan_id": resumedPlan.ID})
	if err != nil {
		t.Fatalf("plan_get failed: %v", err)
	}
	if !strings.Contains(out, resumedPlan.ID) {
		t.Fatalf("plan_get should return the plan, got: %s", out)
	}
	if stub.adoptedPlan != resumedPlan.ID || stub.adoptedSession != newSID {
		t.Fatalf("AdoptSession not called correctly: plan=%q session=%q", stub.adoptedPlan, stub.adoptedSession)
	}

	// Step 2: after re-anchoring, GetActiveBySession for the new session finds
	// the plan — this is the exact call resolvePositionalStep makes.
	p, err := stub.GetActiveBySession(ctx, newSID)
	if err != nil {
		t.Fatalf("GetActiveBySession after adopt failed: %v", err)
	}
	if p == nil {
		t.Fatal("GetActiveBySession must return the adopted plan for the new session (got nil) — resolvePositionalStep would throw 'no active plan'")
	}
	if p.ID != resumedPlan.ID {
		t.Fatalf("GetActiveBySession returned wrong plan: got %q, want %q", p.ID, resumedPlan.ID)
	}

	// Step 3: the plan has the expected steps, so a positional ref "2" resolves.
	if len(p.Steps) != 2 {
		t.Fatalf("adopted plan should have 2 steps, got %d", len(p.Steps))
	}
	if p.Steps[1].ID != "step-2-uuid" {
		t.Fatalf("step 2 resolved to %q, want step-2-uuid", p.Steps[1].ID)
	}

	// Step 4: the old session no longer owns the plan (no bleed back).
	old, err := stub.GetActiveBySession(ctx, oldSID)
	if err != nil {
		t.Fatalf("GetActiveBySession for old session failed: %v", err)
	}
	if old != nil {
		t.Fatalf("old session must no longer own the plan after adoption, got %+v", old)
	}
}
