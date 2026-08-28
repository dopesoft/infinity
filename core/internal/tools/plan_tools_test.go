package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/turnctx"
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
	_, err := resolveStepRef(context.Background(), nilStore, "2'}},")
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
		ID:         "5145c6e7-daa0-4b93-ad0b-65e995b3fad4",
		SessionID:  oldSID,
		Status:     plan.PlanPaused,
		ApprovedAt: ptrTime(time.Now()), // an approved plan may be resumed across sessions
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

	approvedAt := time.Now()
	resumedPlan := &plan.Plan{
		ID:         "5145c6e7-daa0-4b93-ad0b-65e995b3fad4",
		SessionID:  oldSID,
		Status:     plan.PlanPaused,
		ApprovedAt: &approvedAt, // only a plan the boss approved may be resumed
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

// TestCrossSessionResumeRefusesUnapprovedPlan is the consent half of the
// resume contract (2026-08-26: "I didn't even get a chance to understand what
// it was"). A plan the boss never approved is returned for READ-BACK, never
// adopted into the new session.
func TestCrossSessionResumeRefusesUnapprovedPlan(t *testing.T) {
	oldSID := "83dce231-bc6a-4e69-81e0-d6c452d3daa4"
	newSID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	unapproved := &plan.Plan{
		ID:        "5145c6e7-daa0-4b93-ad0b-65e995b3fad4",
		SessionID: oldSID,
		Status:    plan.PlanProposed,
		Steps:     []plan.Step{{ID: "step-1-uuid", Title: "Distill the book"}},
	}
	stub := &smartResumePlanGetter{
		plansByID:      map[string]*plan.Plan{unapproved.ID: unapproved},
		plansBySession: map[string]*plan.Plan{oldSID: unapproved},
	}
	out, err := (&planGet{store: stub}).Execute(WithSessionID(context.Background(), newSID), map[string]any{"plan_id": unapproved.ID})
	if err != nil {
		t.Fatalf("plan_get failed: %v", err)
	}
	if stub.adoptedPlan != "" {
		t.Fatalf("an unapproved plan must NOT be adopted, got adopt of %q", stub.adoptedPlan)
	}
	if !strings.Contains(out, `"unapproved":true`) || !strings.Contains(out, "Read it back") {
		t.Fatalf("the model must be told to read the plan back and ask, got: %s", out)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// ── Phase 6: the gate must never block RESUMING (2026-08-28) ────────────────
//
// A 14-minute coding run was killed, its plan step went 'failed' and the plan
// paused. The boss said "please continue the build and finish up". The turn
// classified as `discuss`, so the consent gate refused plan_update and Jarvis
// reported that he had "blocked me from reopening the step". Two of the four
// causes live in this file: plan_approve returned early on an already-active
// plan WITHOUT opening the stance, and there was no non-gated verb for
// "continue" at all.

// resumeStore is a planResumer / planApprover backed by in-memory plans, so
// the consent rules below are provable without a database. It records every
// MarkStep so a test can assert what was (and was not) reopened.
type resumeStore struct {
	byID      map[string]*plan.Plan
	bySession map[string]*plan.Plan
	proposed  map[string]*plan.Plan
	anyActive *plan.Plan

	marked         []markCall
	approved       string
	adoptedPlan    string
	adoptedSession string
	stepRuns       int
}

type markCall struct{ stepID, status, summary string }

func newResumeStore() *resumeStore {
	return &resumeStore{
		byID:      map[string]*plan.Plan{},
		bySession: map[string]*plan.Plan{},
		proposed:  map[string]*plan.Plan{},
	}
}

func (s *resumeStore) add(sessionID string, p *plan.Plan) *plan.Plan {
	p.SessionID = sessionID
	s.byID[p.ID] = p
	if p.Status == plan.PlanProposed {
		s.proposed[sessionID] = p
	} else {
		s.bySession[sessionID] = p
	}
	return p
}

func (s *resumeStore) Get(_ context.Context, id string) (*plan.Plan, error) { return s.byID[id], nil }
func (s *resumeStore) GetActiveBySession(_ context.Context, sid string) (*plan.Plan, error) {
	return s.bySession[sid], nil
}
func (s *resumeStore) GetProposedBySession(_ context.Context, sid string) (*plan.Plan, error) {
	return s.proposed[sid], nil
}
func (s *resumeStore) GetAnyActive(_ context.Context) (*plan.Plan, error) { return s.anyActive, nil }
func (s *resumeStore) AdoptSession(_ context.Context, planID, sessionID string) error {
	s.adoptedPlan, s.adoptedSession = planID, sessionID
	return nil
}
func (s *resumeStore) SetStepRun(_ context.Context, _, _ string) error { s.stepRuns++; return nil }
func (s *resumeStore) Approve(_ context.Context, planID, _ string) (*plan.Plan, error) {
	s.approved = planID
	p := s.byID[planID]
	if p != nil {
		p.Status = plan.PlanActive
	}
	return p, nil
}
func (s *resumeStore) MarkStep(_ context.Context, stepID, status, summary string) (*plan.Plan, error) {
	s.marked = append(s.marked, markCall{stepID, status, summary})
	for _, p := range s.byID {
		for i := range p.Steps {
			if p.Steps[i].ID == stepID {
				p.Steps[i].Status = status
				if status == plan.StepInProgress && p.Status == plan.PlanPaused {
					p.Status = plan.PlanActive // mirrors store.recompute
				}
				return p, nil
			}
		}
	}
	return nil, nil
}

// killedBuildPlan is the live shape of plan 2f1508a2: step 1 failed when its
// coding run was killed, step 2 was skipped, steps 3-4 never started, and the
// whole plan paused. The boss approved it before any of that happened.
func killedBuildPlan(id string) *plan.Plan {
	return &plan.Plan{
		ID:         id,
		Status:     plan.PlanPaused,
		ApprovedAt: ptrTime(time.Now().Add(-time.Hour)),
		Title:      "Make Jarvis finish long coding tasks",
		Steps: []plan.Step{
			{ID: "step-1", Idx: 0, Title: "Give the job its own lifetime", Status: plan.StepFailed},
			{ID: "step-2", Idx: 1, Title: "Salvage the receipt", Status: plan.StepSkipped},
			{ID: "step-3", Idx: 2, Title: "Add worker liveness safeguards", Status: plan.StepPending},
			{ID: "step-4", Idx: 3, Title: "Enforce proof-gated completion", Status: plan.StepPending},
		},
	}
}

func stanceCtx(sessionID string) (context.Context, *turnctx.StanceHolder) {
	h := turnctx.NewStanceHolder()
	h.Set(turnctx.StanceDiscuss, "read as conversation")
	return turnctx.WithStance(WithSessionID(context.Background(), sessionID), h), h
}

// TestPlanApprove_AlreadyApprovedPlanStillOpensTheGate is mechanism #1. With no
// proposal to approve, plan_approve fell through to GetActiveBySession and
// returned the plan — BEFORE the h.Set(StanceWork) that is the only designed
// way the consent gate ever opens besides the classifier. So "go ahead" on a
// paused build left every following plan_update refused.
func TestPlanApprove_AlreadyApprovedPlanStillOpensTheGate(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	store := newResumeStore()
	store.add(sid, killedBuildPlan("plan-paused"))
	ctx, h := stanceCtx(sid)

	out, err := (&planApprove{store: store}).Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("plan_approve on an already-approved plan must not error: %v", err)
	}
	if !strings.Contains(out, "plan-paused") || !strings.Contains(out, `"already_active":true`) {
		t.Fatalf("plan_approve should hand back the live plan, got: %s", out)
	}
	if !strings.Contains(out, "plan_resume") {
		t.Fatalf("the model must be told how to carry on, got: %s", out)
	}
	if got, why := h.Get(); got != turnctx.StanceWork {
		t.Fatalf("stance = %q (%s), want work — the gate stayed shut on an approved build", got, why)
	}
	if latched, _ := h.Latched(); !latched {
		t.Fatal("his go must latch the turn so a later chatty steer can't shut the gate again")
	}
}

// TestPlanApprove_UnapprovedActivePlanDoesNotOpenTheGate: the gate only opens
// for work he actually approved. A plan with no approved_at is not his go.
func TestPlanApprove_UnapprovedActivePlanDoesNotOpenTheGate(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	store := newResumeStore()
	p := killedBuildPlan("plan-unapproved")
	p.ApprovedAt = nil
	store.add(sid, p)
	ctx, h := stanceCtx(sid)

	if _, err := (&planApprove{store: store}).Execute(ctx, map[string]any{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := h.Get(); got != turnctx.StanceDiscuss {
		t.Fatalf("stance = %q, want discuss — an unapproved plan is not the boss's go", got)
	}
}

// TestPlanResume_ReopensTheKilledStepAndOpensTheGate is mechanism #2, the whole
// point of the tool: "continue the build and finish up" now has a verb, it puts
// the agent back on the step that was cut off, and it opens the gate for the
// tools that step needs.
func TestPlanResume_ReopensTheKilledStepAndOpensTheGate(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	store := newResumeStore()
	store.add(sid, killedBuildPlan("plan-paused"))
	ctx, h := stanceCtx(sid)

	out, err := (&planResume{store: store}).Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("plan_resume failed: %v", err)
	}
	if len(store.marked) != 1 || store.marked[0].stepID != "step-1" || store.marked[0].status != plan.StepInProgress {
		t.Fatalf("plan_resume must reopen the step the run was killed on, got %+v", store.marked)
	}
	if !strings.Contains(out, `"was":"failed"`) || !strings.Contains(out, "step-1") {
		t.Fatalf("the model must see which step it is back on, got: %s", out)
	}
	if got, _ := h.Get(); got != turnctx.StanceWork {
		t.Fatalf("stance = %q, want work — resuming is the boss's go", got)
	}
	if latched, _ := h.Latched(); !latched {
		t.Fatal("resuming must latch the turn against a mid-build demotion")
	}
	// The live spinner rides runs.BeginGlobal exactly as plan_update's start
	// branch does; with no tracker configured (unit test) it hands back an
	// empty run id and SetStepRun is correctly skipped, so stepRuns stays 0.
	if store.stepRuns != 0 {
		t.Fatalf("no run tracker is configured, so no step run should be recorded, got %d", store.stepRuns)
	}
	if p := store.byID["plan-paused"]; p.Status != plan.PlanActive {
		t.Fatalf("plan status after resume = %q, want active", p.Status)
	}
}

// TestPlanResume_RefusesAnUnapprovedProposal is the load-bearing safety rule.
// plan_resume is NOT consent-gated, so it is the one verb that could turn a
// proposal the boss never saw through into running work. It must not: the
// proposal flow is deliberate (2026-08-26, "I didn't even get a chance to
// understand what it was").
func TestPlanResume_RefusesAnUnapprovedProposal(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	store := newResumeStore()
	store.add(sid, &plan.Plan{
		ID:     "plan-proposal",
		Status: plan.PlanProposed,
		Title:  "Rewrite the billing service",
		Steps:  []plan.Step{{ID: "step-1", Idx: 0, Title: "Rip out Stripe", Status: plan.StepPending}},
	})
	ctx, h := stanceCtx(sid)

	out, err := (&planResume{store: store}).Execute(ctx, map[string]any{"plan_id": "plan-proposal"})
	if err != nil {
		t.Fatalf("plan_resume should refuse politely, not error: %v", err)
	}
	if !strings.Contains(out, `"unapproved":true`) {
		t.Fatalf("a proposal must be refused as unapproved, got: %s", out)
	}
	if len(store.marked) != 0 {
		t.Fatalf("plan_resume started an unapproved proposal: %+v", store.marked)
	}
	if store.adoptedPlan != "" {
		t.Fatalf("an unapproved proposal must not be adopted, got %q", store.adoptedPlan)
	}
	if got, _ := h.Get(); got != turnctx.StanceDiscuss {
		t.Fatalf("stance = %q, want discuss — resuming a proposal must never open the gate", got)
	}
}

// TestPlanResume_RefusesAClosedPlan: "continue" on something finished or killed
// is not a resume; the agent must say so rather than reopening history.
func TestPlanResume_RefusesAClosedPlan(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, status := range []string{plan.PlanCompleted, plan.PlanCancelled} {
		store := newResumeStore()
		p := killedBuildPlan("plan-" + status)
		p.Status = status
		store.add(sid, p)
		ctx, h := stanceCtx(sid)

		out, err := (&planResume{store: store}).Execute(ctx, map[string]any{"plan_id": p.ID})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", status, err)
		}
		if !strings.Contains(out, status) || len(store.marked) != 0 {
			t.Fatalf("%s plan must not be reopened, out=%s marked=%+v", status, out, store.marked)
		}
		if got, _ := h.Get(); got != turnctx.StanceDiscuss {
			t.Fatalf("%s: stance = %q, want discuss", status, got)
		}
	}
}

// TestPlanResume_AllStepsDoneSaysSo: nothing left to reopen must read as
// "it's finished", never as a silently-successful resume that starts nothing.
func TestPlanResume_AllStepsDoneSaysSo(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	store := newResumeStore()
	store.add(sid, &plan.Plan{
		ID: "plan-finished", Status: plan.PlanActive, ApprovedAt: ptrTime(time.Now()),
		Steps: []plan.Step{
			{ID: "step-1", Idx: 0, Title: "One", Status: plan.StepDone},
			{ID: "step-2", Idx: 1, Title: "Two", Status: plan.StepSkipped},
		},
	})
	ctx, _ := stanceCtx(sid)
	out, err := (&planResume{store: store}).Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.marked) != 0 {
		t.Fatalf("nothing should have been reopened, got %+v", store.marked)
	}
	if !strings.Contains(out, "already finished") {
		t.Fatalf("the model must be told there is nothing left, got: %s", out)
	}
}

// TestPlanResume_ExplicitStepAndInProgressStep covers the two remaining picks:
// an explicit step reference wins, and a step already running is not churned.
func TestPlanResume_ExplicitStepAndInProgressStep(t *testing.T) {
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	store := newResumeStore()
	store.add(sid, killedBuildPlan("plan-paused"))
	ctx, _ := stanceCtx(sid)
	if _, err := (&planResume{store: store}).Execute(ctx, map[string]any{"step": "3"}); err != nil {
		t.Fatalf("explicit step resume failed: %v", err)
	}
	if len(store.marked) != 1 || store.marked[0].stepID != "step-3" {
		t.Fatalf("an explicit step ref must win, got %+v", store.marked)
	}

	running := newResumeStore()
	p := killedBuildPlan("plan-running")
	p.Steps[2].Status = plan.StepInProgress
	running.add(sid, p)
	ctx2, h := stanceCtx(sid)
	out, err := (&planResume{store: running}).Execute(ctx2, map[string]any{})
	if err != nil {
		t.Fatalf("resume with a live step failed: %v", err)
	}
	if len(running.marked) != 0 {
		t.Fatalf("a step already running must not be re-marked or double-booked, got %+v", running.marked)
	}
	if !strings.Contains(out, "step-3") {
		t.Fatalf("the live step should be the one resumed, got: %s", out)
	}
	if got, _ := h.Get(); got != turnctx.StanceWork {
		t.Fatalf("stance = %q, want work", got)
	}
}

// TestPlanResume_NoPlanToResume: an honest error, not a silent no-op.
func TestPlanResume_NoPlanToResume(t *testing.T) {
	ctx, h := stanceCtx("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	_, err := (&planResume{store: newResumeStore()}).Execute(ctx, map[string]any{})
	if err == nil {
		t.Fatal("resuming with no plan must be an error, not a quiet success")
	}
	if !strings.Contains(err.Error(), "plan_create") {
		t.Fatalf("the error should say what to do instead, got: %v", err)
	}
	if got, _ := h.Get(); got != turnctx.StanceDiscuss {
		t.Fatalf("stance = %q, want discuss — a failed resume is not consent", got)
	}
}

// TestPlanResume_InteractiveTurnNeverPicksUpAStrangersPlan keeps the 2026-07-02
// cross-session bleed shut on the new verb too: interactive turns get their own
// session's plan or nothing.
func TestPlanResume_InteractiveTurnNeverPicksUpAStrangersPlan(t *testing.T) {
	store := newResumeStore()
	store.anyActive = killedBuildPlan("plan-stranger")
	ctx, _ := stanceCtx("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if _, err := (&planResume{store: store}).Execute(ctx, map[string]any{}); err == nil {
		t.Fatal("an interactive turn must not resume another session's plan")
	}
	if len(store.marked) != 0 {
		t.Fatalf("a stranger's plan was touched: %+v", store.marked)
	}
}

// TestPickResumeStep covers the ordering rule directly: a running step wins,
// otherwise the FIRST unfinished step in plan order (the killed one), and
// nothing when every step is terminal.
func TestPickResumeStep(t *testing.T) {
	p := killedBuildPlan("p")
	got, err := pickResumeStep(p, "")
	if err != nil || got == nil || got.ID != "step-1" {
		t.Fatalf("want the failed step-1 (plan order), got %+v err=%v", got, err)
	}
	p.Steps[3].Status = plan.StepInProgress
	if got, _ = pickResumeStep(p, ""); got == nil || got.ID != "step-4" {
		t.Fatalf("a step already running wins, got %+v", got)
	}
	done := &plan.Plan{ID: "p", Steps: []plan.Step{{ID: "s", Status: plan.StepDone}}}
	if got, err = pickResumeStep(done, ""); err != nil || got != nil {
		t.Fatalf("a fully finished plan has nothing to reopen, got %+v err=%v", got, err)
	}
	if _, err = pickResumeStep(p, "99"); err == nil {
		t.Fatal("an out-of-range step ref must error")
	}
}
