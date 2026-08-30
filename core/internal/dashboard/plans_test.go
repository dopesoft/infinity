package dashboard

import (
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// The board said four plans were running for three days and ten more were
// waiting on the boss since June. None of it was true, and it is the kind of
// lie that is worse than a blank card: he cannot tell a live job from a
// corpse, so he stops trusting the ones that ARE live.
//
// These cases are the actual production rows from 2026-08-29. They encode the
// rule that matters: a plan is only alive while something is still touching
// it. Nothing else - not its status column, not whether a run or a session
// survived to explain it - gets a vote, because in every one of these rows
// that bookkeeping is exactly what went missing.
func TestPlanStalenessTreatsUntouchedPlansAsNotRunning(t *testing.T) {
	now := time.Date(2026, 8, 29, 22, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		status       string
		updatedAt    time.Time
		wantTerminal bool
		wantStale    bool
	}{
		{
			// The four `/workspace/infinity` rows, orphaned by the brain-quota
			// incident. Status 'active', no session id at all, so no run could
			// ever be consulted to move them. Untouched for three days.
			name:      "active plan untouched for three days is not running",
			status:    plan.PlanActive,
			updatedAt: now.Add(-3 * 24 * time.Hour),
			wantStale: true,
		},
		{
			// The oldest of the ten. It sat in the needs-you lane for nearly
			// three months claiming to be paused at a checkpoint.
			name:      "paused plan untouched since June is not waiting on him",
			status:    plan.PlanPaused,
			updatedAt: now.Add(-82 * 24 * time.Hour),
			wantStale: true,
		},
		{
			// The guard must not touch real work. A cron agent turn runs under
			// a 30 minute budget, so a plan mid-step is well inside the window.
			name:      "active plan touched two minutes ago is still running",
			status:    plan.PlanActive,
			updatedAt: now.Add(-2 * time.Minute),
			wantStale: false,
		},
		{
			// The boundary, from the slow side: a step that has taken longer
			// than any legitimate cron turn still gets its full 45 minutes.
			name:      "active plan at 44 minutes is still running",
			status:    plan.PlanActive,
			updatedAt: now.Add(-44 * time.Minute),
			wantStale: false,
		},
		{
			name:      "active plan at 46 minutes has gone quiet",
			status:    plan.PlanActive,
			updatedAt: now.Add(-46 * time.Minute),
			wantStale: true,
		},
		{
			// A proposal is waiting on HIM, and it waits as long as it takes.
			// Age is not evidence about a question nobody has answered yet, so
			// staleness must never quietly retire one. Writing this case is
			// what caught the guard doing exactly that.
			name:      "old proposal is still a live question",
			status:    plan.PlanProposed,
			updatedAt: now.Add(-9 * 24 * time.Hour),
			wantStale: false,
		},
		{
			name:         "completed plan is terminal, never stale",
			status:       plan.PlanCompleted,
			updatedAt:    now.Add(-30 * 24 * time.Hour),
			wantTerminal: true,
		},
		{
			name:         "failed plan is terminal, never stale",
			status:       plan.PlanFailed,
			updatedAt:    now.Add(-30 * 24 * time.Hour),
			wantTerminal: true,
		},
		{
			name:         "cancelled plan is terminal, never stale",
			status:       plan.PlanCancelled,
			updatedAt:    now.Add(-30 * 24 * time.Hour),
			wantTerminal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terminal, stale := planStaleness(tc.status, tc.updatedAt, now)
			if terminal != tc.wantTerminal {
				t.Fatalf("terminal = %v, want %v", terminal, tc.wantTerminal)
			}
			if stale != tc.wantStale {
				t.Fatalf("stale = %v, want %v", stale, tc.wantStale)
			}
			// A terminal plan can never also be stale: it finished, and
			// "finished a while ago" is history, not a stall.
			if terminal && stale {
				t.Fatal("a terminal plan must never be reported stale")
			}
		})
	}
}
