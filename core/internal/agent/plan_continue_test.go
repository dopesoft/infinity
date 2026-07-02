package agent

import (
	"context"
	"errors"
	"testing"
)

// isPlanTool decides whether a tool call means "this turn is doing plan work",
// which is what gates the keep-going nudge. Pin it so a rename of the plan verbs
// (or someone adding plan_get to the mutating set) can't silently break the
// backstop or make it fire on a read.
func TestIsPlanTool(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"plan_create", true},
		{"plan_update", true},
		{"plan_verify", true},
		{"plan_revise", true},
		{"plan_cancel", true},
		{"todo_write", true},
		{"plan_get", false}, // read-only — doesn't mean work is happening
		{"websearch", false},
		{"recall", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isPlanTool(c.name); got != c.want {
			t.Errorf("isPlanTool(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// fakeChecker is a stand-in for *plan.Store so we can exercise the loop's
// decision without a database.
type fakeChecker struct {
	unfinished bool
	err        error
	calls      int
}

func (f *fakeChecker) HasUnfinishedPlan(ctx context.Context, sessionID string) (bool, error) {
	f.calls++
	return f.unfinished, f.err
}

// hasUnfinishedPlan is the conservative, nil-safe gate: it must return false
// (fall back to the prior stop-and-reaper behavior) whenever the feature is off,
// no checker is wired, or the probe errors — and only true when a checker
// genuinely reports unfinished work.
func TestHasUnfinishedPlan(t *testing.T) {
	t.Run("no checker wired → false", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_CONTINUE", "")
		l := &Loop{}
		if l.hasUnfinishedPlan(context.Background(), "s1") {
			t.Fatal("want false when no checker is wired")
		}
	})

	t.Run("checker reports unfinished → true", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_CONTINUE", "")
		l := &Loop{}
		fc := &fakeChecker{unfinished: true}
		l.SetPlanChecker(fc)
		if !l.hasUnfinishedPlan(context.Background(), "s1") {
			t.Fatal("want true when checker reports unfinished work")
		}
		if fc.calls != 1 {
			t.Fatalf("checker calls = %d, want 1", fc.calls)
		}
	})

	t.Run("checker reports finished → false", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_CONTINUE", "")
		l := &Loop{}
		l.SetPlanChecker(&fakeChecker{unfinished: false})
		if l.hasUnfinishedPlan(context.Background(), "s1") {
			t.Fatal("want false when checker reports no unfinished work")
		}
	})

	t.Run("probe error → false (never blocks the turn)", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_CONTINUE", "")
		l := &Loop{}
		l.SetPlanChecker(&fakeChecker{unfinished: true, err: errors.New("db down")})
		if l.hasUnfinishedPlan(context.Background(), "s1") {
			t.Fatal("want false when the probe errors — a DB blip must not force a nudge")
		}
	})

	t.Run("disabled via env → false, checker not consulted", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_CONTINUE", "off")
		l := &Loop{}
		fc := &fakeChecker{unfinished: true}
		l.SetPlanChecker(fc)
		if l.hasUnfinishedPlan(context.Background(), "s1") {
			t.Fatal("want false when INFINITY_PLAN_CONTINUE=off")
		}
		if fc.calls != 0 {
			t.Fatalf("checker consulted while disabled (calls=%d)", fc.calls)
		}
	})
}

// The directive is what the model actually reads at the stop seam. If it ever
// goes empty the backstop silently becomes a no-op, so pin that it carries the
// imperative "keep going / execute the plan" intent.
func TestPlanContinueDirectiveNonEmpty(t *testing.T) {
	if len(planContinueDirective) < 100 {
		t.Fatalf("planContinueDirective looks empty/too short (%d chars)", len(planContinueDirective))
	}
}
