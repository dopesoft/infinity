package agent

import (
	"context"
	"log"
	"os"
	"strings"
	"time"
)

// Plan-continuation backstop — the STRUCTURAL fix for the "drafts a plan, then
// quits" pathology (the 2026-07-02 report failure: the model called plan_create
// + plan_update(step 0 in_progress) and then ended its turn with no further
// work; the step dead-spun for 11 minutes until the reaper failed it and nothing
// was ever delivered).
//
// soul.md tells Jarvis to see multi-step tasks through, but the runtime brain
// (gpt-5.x) routinely drops standing prose and treats "I laid out the plan" as
// "I'm done for this turn". This is the part the model can't forget: when a turn
// is about to END and the session has an ACTIVE plan with unfinished steps that
// this turn actually touched, the loop injects a keep-going directive and runs
// another pass so the agent executes the plan instead of stopping. Bounded per
// turn so a genuinely-blocked plan still ends, and gated on the plan having been
// touched THIS turn so a stale plan from an earlier turn never hijacks an
// unrelated reply. Off via INFINITY_PLAN_CONTINUE=off.

// maxPlanContinuePerTurn bounds the keep-going nudges. Three gives enough grit
// to break the "stop cold after drafting" reflex (and to survive a model that
// stops again after one step) without turning a genuinely-parked plan into a
// grind. The LoopGate (50 calls / 5 min) and maxTurnSegments are the hard
// runaway backstops underneath this.
const maxPlanContinuePerTurn = 3

// PlanContinuationChecker reports whether a session has an active plan with
// unfinished work. Implemented by *plan.Store (HasUnfinishedPlan); declared here
// so the agent package doesn't drag a pgx/plan import into the loop. Nil-safe:
// when unset the loop simply never nudges and the pre-existing behavior (stop
// after drafting, reaper cleans up later) stands.
type PlanContinuationChecker interface {
	HasUnfinishedPlan(ctx context.Context, sessionID string) (bool, error)
}

// PlanSettler closes out a session's plan the instant a FOREGROUND turn ends,
// mirroring what cron already does on its path (executor_agent FinalizeSession)
// and what background builds do via OnDone. Without it, a foreground chat plan
// left with an in_progress step just sat there until the 10-min plan-step reaper
// FALSELY failed it ("stalled — no result recorded") — the 2026-07-02 incident
// where the report was written but the plan showed "Research the market ❌".
// Implemented by *plan.Store.FinalizeSession: an in_progress step whose run is
// still 'running' at turn-end becomes 'skipped' ("I stopped here"), NOT failed;
// a step whose run already errored stays failed. Nil-safe + hot-swap.
type PlanSettler interface {
	FinalizeSession(ctx context.Context, sessionID string) (int, error)
}

// planContinueDisabled lets the boss kill the reflex (INFINITY_PLAN_CONTINUE=off).
func planContinueDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INFINITY_PLAN_CONTINUE"))) {
	case "off", "false", "0", "no":
		return true
	}
	return false
}

// isPlanTool reports whether a tool call is one that creates or advances the
// session's durable plan — the signal that THIS turn is doing plan work (so a
// premature stop is the "drafted then quit" pathology, not an unrelated reply
// that happens to have a stale plan lying around). plan_get is read-only and
// excluded; todo_write writes the same mem_plans/mem_plan_steps substrate (see
// tools/todo_tools.go — "a todo checklist and a plan are the SAME thing") so it
// counts.
func isPlanTool(name string) bool {
	if name == "todo_write" {
		return true
	}
	// plan_create / plan_update / plan_verify / plan_revise / plan_cancel — the
	// mutating plan verbs. plan_get is read-only.
	return strings.HasPrefix(name, "plan_") && name != "plan_get"
}

// planContinueDirective is injected as a user-role message so the model treats
// it as a fresh, top-of-mind instruction — the same trick self-heal uses. It is
// deliberately imperative: laying out a plan is not doing the work.
const planContinueDirective = `STOP — do not end your turn here. You have an active plan with steps still to do, and you just stopped after setting it up (or after one step). Laying out a plan is NOT doing the work. The boss asked you to DO this, not to describe how you would.

This runtime note only applies to a plan the boss APPROVED (an active plan). His latest message outranks it: if he asked to discuss, hold, change course, or stop, do that instead of continuing.

Keep going NOW:
1. Look at your active plan (it's in your context, or call plan_get). Take the next unfinished step.
2. Actually EXECUTE it with your tools — search, fetch, read, write, build, whatever the step needs. Mark it in_progress when you start, and done (with plan_verify evidence when required) once it's really finished and the output exists.
3. Continue through the remaining steps until the plan is complete and the deliverable actually exists.

Stop and hand back to the boss ONLY if you genuinely need a decision or a credential that only he can give — and if so, say exactly what you need, with the work teed up. Otherwise do not stop until it's done.`

// planContinuationChecker snapshots the injected checker under an RWMutex.
func (l *Loop) planContinuationChecker() PlanContinuationChecker {
	if l == nil {
		return nil
	}
	l.planMu.RLock()
	defer l.planMu.RUnlock()
	return l.planChecker
}

// SetPlanChecker installs (or replaces) the active-plan checker used by the
// plan-continuation backstop. Safe to call after agent.New(); the loop snapshots
// it under an RWMutex on every Run. Nil is fine (feature off).
func (l *Loop) SetPlanChecker(c PlanContinuationChecker) {
	if l == nil {
		return
	}
	l.planMu.Lock()
	l.planChecker = c
	l.planMu.Unlock()
}

// SetPlanSettler installs (or replaces) the foreground plan settle-on-turn-end
// mechanic. Safe to call after agent.New(); nil is fine (feature off).
func (l *Loop) SetPlanSettler(s PlanSettler) {
	if l == nil {
		return
	}
	l.planMu.Lock()
	l.planSettler = s
	l.planMu.Unlock()
}

func (l *Loop) planSettlerFn() PlanSettler {
	if l == nil {
		return nil
	}
	l.planMu.RLock()
	defer l.planMu.RUnlock()
	return l.planSettler
}

// settlePlanOnTurnEnd closes out the session's plan the instant a foreground
// turn ends, so an in_progress step is settled cleanly ('skipped'/'done' — see
// FinalizeSession) instead of dead-spinning until the reaper false-fails it.
// Runs on a DETACHED context (the turn ctx may already be canceled/expired on
// the timeout/interrupt paths) with a short budget, mirroring cron's settle.
// Best-effort + nil-safe; skips synthetic session ids.
func (l *Loop) settlePlanOnTurnEnd(sessionID string) {
	settler := l.planSettlerFn()
	if settler == nil || IsSyntheticSessionID(sessionID) || strings.TrimSpace(sessionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := settler.FinalizeSession(ctx, sessionID); err != nil {
		log.Printf("plan settle on turn-end: session=%s err=%v", sessionID, err)
	}
}

// hasUnfinishedPlan reports whether the loop should nudge the model to keep
// executing this session's plan instead of ending the turn. Conservative and
// nil-safe: disabled by env, no checker wired, or a DB error all → false (fall
// back to the prior stop-and-let-the-reaper-handle-it behavior).
func (l *Loop) hasUnfinishedPlan(ctx context.Context, sessionID string) bool {
	if planContinueDisabled() {
		return false
	}
	pc := l.planContinuationChecker()
	if pc == nil {
		return false
	}
	ok, err := pc.HasUnfinishedPlan(ctx, sessionID)
	if err != nil {
		log.Printf("plan-continue check: session=%s err=%v", sessionID, err)
		return false
	}
	return ok
}
