package cron

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// ReconcileReaped was the ONE place that already refused to equate "killed" with
// "failed" — it re-derived a real outcome from recorded signal instead of
// stamping 'error'. It only did it for crons. Plan steps and detached coding
// jobs got orphaned exactly the same way and got the bare 'error', which the
// plan layer then read as proof the work had failed. These tests pin the
// generalisation, and pin that generalising it did not soften anything.

func TestDefaultReconcileAges_CoversEveryOrphanableKind(t *testing.T) {
	ages := DefaultReconcileAges(45*time.Minute, 10*time.Minute)

	for _, kind := range []string{
		string(runs.KindCron),
		string(runs.KindPlanStep),
		string(runs.KindBackgroundBuild),
		string(runs.KindCodeAgent),
	} {
		if _, ok := ages[kind]; !ok {
			t.Fatalf("%q gets orphaned by a restart the same way a cron does and needs the same honest re-derivation", kind)
		}
	}
	if ages[string(runs.KindCron)] != 45*time.Minute {
		t.Fatalf("a cron must keep its 45-min grace (longer than the 30-min job timeout): %v", ages[string(runs.KindCron)])
	}
	// The step reaper stamps a bare 'error' at stepReapAge and the plan layer
	// cascades that into a failed step. Reconciling at a LONGER age would always
	// lose the race, making the honest path unreachable - built but not wired.
	if ages[string(runs.KindPlanStep)] > 10*time.Minute {
		t.Fatalf("plan.step must reconcile at or before its own reaper age or it can never win the race: %v", ages[string(runs.KindPlanStep)])
	}
	// Coding kinds are exempt from the blanket reaper precisely because a clock
	// can't tell a working build from a dead one, so their grace must comfortably
	// exceed background_build's 60-minute ceiling.
	for _, k := range runs.CodingKinds() {
		if ages[k] < time.Hour {
			t.Fatalf("%q must outlive a legitimate long build before being reconciled: %v", k, ages[k])
		}
	}
}

func TestDefaultReconcileAges_CodingNeverShorterThanTheCronBudget(t *testing.T) {
	ages := DefaultReconcileAges(6*time.Hour, time.Minute)
	for _, k := range runs.CodingKinds() {
		if ages[k] < 6*time.Hour {
			t.Fatalf("%q must never get a shorter grace than the configured run budget: %v", k, ages[k])
		}
	}
}

// The floor corrects classifyOutcome's optimistic DEFAULT for a run that left no
// receipt at all. It must only ever move did_work -> stopped_early: every
// escalation the classifier can reach has to survive untouched, above all the
// hard-HTTP-failure veto that turns a swallowed 401 into a red run.
func TestReapedOutcomeFloor_OnlyDowngradesTheOptimisticDefault(t *testing.T) {
	if got := reapedOutcomeFloor(OutcomeDidWork); got != OutcomeStoppedEarly {
		t.Fatalf("a run with no receipt must not claim it did the work: got %q", got)
	}
	for _, o := range []Outcome{OutcomeFailed, OutcomeNeedsYou, OutcomeNothingNeeded, OutcomeStoppedEarly} {
		if got := reapedOutcomeFloor(o); got != o {
			t.Fatalf("%q must pass through unchanged (escalations must never be softened): got %q", o, got)
		}
	}
}

// The no-false-greens guarantee, restated at this seam: whatever else the
// reconciler does, a run whose session logged a hard outbound failure — or that
// carries a real error — still classifies FAILED, and a failed reconcile writes
// status 'error' with NO stopped_reason, so it still fails its plan step.
func TestReconcile_FailureStaysAFailure(t *testing.T) {
	if got := classifyOutcome(t.Context(), nil, "", RunSummary{}, errors.New("boom")); got != OutcomeFailed {
		t.Fatalf("an execution error must still classify failed: %q", got)
	}
	if got := reapedOutcomeFloor(classifyOutcome(t.Context(), nil, "", RunSummary{}, errors.New("boom"))); got != OutcomeFailed {
		t.Fatalf("the reaped floor must not launder a failure: %q", got)
	}
	// The veto's SQL predicate is untouched by this work - it is the guard that
	// stops the OPPOSITE error (a false green) and must stay exactly as it is.
	for _, want := range []string{"status = 0", "401", "403", "429", "status >= 500"} {
		if !strings.Contains(hardHTTPFailureWhere, want) {
			t.Fatalf("the hard-HTTP-failure veto lost %q - that guard stops false greens and must not be weakened", want)
		}
	}
}

// Scoping. The meta.outcome guard bounds the CRON set (its live finalize always
// stamps one); it bounds nothing else, because no other kind stamps an outcome.
// Without the still-open clause this would re-sweep every plan.step in history
// on every 2-minute tick and stamp "interrupted" on runs that finished fine.
func TestReconcileKindSQL_NonCronOnlySweepsRowsNothingEverClosed(t *testing.T) {
	if !strings.Contains(reconcileKindSQL, "%s") {
		t.Fatal("the scope clause must be substitutable per kind")
	}
	if stillOpenOnly != "AND r.status = 'running'" {
		t.Fatalf("orphaned means the owning process never closed the row: %q", stillOpenOnly)
	}
	if !strings.Contains(reconcileKindSQL, "(r.meta->>'outcome') IS NULL") {
		t.Fatal("the idempotence guard must stay - a second pass has to be a no-op")
	}
	// The in-flight-turn gate: age alone is a lie for a step inside a live turn
	// (the 2026-07-02 "Research the market ❌ while the turn was still working").
	if !strings.Contains(reconcileKindSQL, "tn.status = 'in_flight'") {
		t.Fatal("a plan.step whose session still has a live turn is NOT orphaned, however old it is")
	}
	// Without a session id classifyOutcome cannot run its HTTP-failure veto, so
	// a run whose 401 we can't see would classify green.
	if !strings.Contains(reconcileKindSQL, "pl.session_id") {
		t.Fatal("the plan graph must supply the session id when meta has none, or the false-green veto is disarmed")
	}
}

// The cron-only bookkeeping must stay cron-only: a plan step is not a cron and
// must never write a cron's last-run row or post a cron inbox card.
func TestReconcileKind_CronOnlyBookkeepingIsGuarded(t *testing.T) {
	// nil scheduler is the cheapest proof the guard is the FIRST thing checked;
	// a nil-pool scheduler proves the query is never attempted either.
	var s *Scheduler
	if n, err := s.ReconcileReaped(t.Context(), DefaultReconcileAges(time.Minute, time.Minute)); n != 0 || err != nil {
		t.Fatalf("nil scheduler must no-op: %d %v", n, err)
	}
	s2 := &Scheduler{}
	if n, err := s2.ReconcileReaped(t.Context(), nil); n != 0 || err != nil {
		t.Fatalf("an empty scope must no-op rather than sweep everything: %d %v", n, err)
	}
}
