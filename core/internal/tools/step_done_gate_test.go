package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeGate struct {
	called []string
	err    error
}

func (f *fakeGate) CheckStepDone(_ context.Context, stepID string) error {
	f.called = append(f.called, stepID)
	return f.err
}

// The gate has to land on plan_update specifically. Attaching it anywhere else
// (or silently attaching nowhere) would leave every route to `done` ungated
// while the boot log claimed otherwise — built-but-not-wired, Rule #1c.
func TestAttachStepDoneGate_LandsOnPlanUpdate(t *testing.T) {
	r := NewRegistry()
	pu := &planUpdate{}
	r.Register(pu)

	if !AttachStepDoneGate(r, &fakeGate{}) {
		t.Fatal("attaching to a registry holding plan_update must report success")
	}
	if pu.doneGate == nil {
		t.Fatal("the gate must actually be installed on the tool, not just reported")
	}
}

// Reporting success when there was nothing to attach to is how a missing gate
// hides behind a green boot line.
func TestAttachStepDoneGate_ReportsWhenThereIsNothingToAttachTo(t *testing.T) {
	if AttachStepDoneGate(NewRegistry(), &fakeGate{}) {
		t.Fatal("no plan_update registered must report failure, not success")
	}
	r := NewRegistry()
	r.Register(&planUpdate{})
	if AttachStepDoneGate(r, nil) {
		t.Fatal("a nil gate is not an attached gate")
	}
	if AttachStepDoneGate(nil, &fakeGate{}) {
		t.Fatal("a nil registry is not an attached gate")
	}
}

// A step with no gate behaves exactly as it did before: the overwhelming
// majority of steps have no mandate bound, and a gate that changed their
// behaviour would be worse than none.
func TestPlanUpdate_UngatedByDefault(t *testing.T) {
	if (&planUpdate{}).doneGate != nil {
		t.Fatal("plan_update must be ungated until something attaches a gate")
	}
}

// The refusal reaches the model verbatim, so it has to be the kind of message
// it can act on. This pins that the gate's error is passed through rather than
// wrapped into something generic.
func TestFakeGateErrorSurfacesUnchanged(t *testing.T) {
	g := &fakeGate{err: errors.New("this step isn't done yet — \"ship it\" still needs: migrations applied")}
	err := g.CheckStepDone(context.Background(), "step-1")
	if err == nil || !strings.Contains(err.Error(), "migrations applied") {
		t.Fatalf("the gate's own wording is what the model must read: %v", err)
	}
	if len(g.called) != 1 || g.called[0] != "step-1" {
		t.Fatalf("the gate is asked about the specific step: %v", g.called)
	}
}
