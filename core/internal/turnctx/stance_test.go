package turnctx

import (
	"context"
	"testing"
	"time"
)

// Why these tests exist: 2026-08-28. A long coding job was killed, its plan
// paused, and the boss said "please continue the build and finish up". Part of
// what went wrong is that a mid-turn message re-classifies the stance and
// StanceHolder.Set overwrote it unconditionally — so one chatty message during
// an approved build could flip the turn back to `discuss`, which shuts the
// consent gate AND switches off self-heal, plan-continuation and the verify
// pass (agent/loop.go). Escalation is now one-way for the life of a turn, and
// the rule lives in the holder so no caller can forget it.

func TestSetAppliesEveryTransitionOnAnUnlatchedTurn(t *testing.T) {
	h := NewStanceHolder()
	// A turn that genuinely starts as a conversation must still be able to
	// BECOME a conversation after a first unknown reading, and to go back and
	// forth before any work has actually happened.
	for _, st := range []Stance{StanceDiscuss, StanceWork, StanceDiscuss, StanceUnclear} {
		if !h.Set(st, "reading") {
			t.Fatalf("unlatched holder refused %q; only a latched work turn may refuse", st)
		}
		if got, _ := h.Get(); got != st {
			t.Fatalf("stance = %q, want %q", got, st)
		}
	}
}

func TestWorkToolLatchesTheTurnAgainstDemotion(t *testing.T) {
	h := NewStanceHolder()
	h.Set(StanceWork, "boss said build it")
	h.MarkWorked("code_agent")

	if latched, why := h.Latched(); !latched || why != "code_agent" {
		t.Fatalf("running a work tool must latch the turn, got latched=%v why=%q", latched, why)
	}
	// The chatty mid-build steer.
	if applied := h.Set(StanceDiscuss, "asking how it's going"); applied {
		t.Fatal("a mid-turn steer must NOT demote a turn that has already run a work tool")
	}
	if got, _ := h.Get(); got != StanceWork {
		t.Fatalf("stance after refused demotion = %q, want work", got)
	}
	if n, why := h.RefusedDemotions(); n != 1 || why != "asking how it's going" {
		t.Fatalf("the refusal must be recorded, got n=%d why=%q", n, why)
	}
	// Escalation still applies, and so does anything that is not a demotion.
	if !h.Set(StanceWork, "still working") {
		t.Fatal("a work re-reading must still apply on a latched turn")
	}
	if !h.Set(StanceUnclear, "mixed") {
		t.Fatal("only work->discuss is refused; unclear must still apply")
	}
}

func TestMarkWorkedDoesNotLatchAnUnansweredClassifier(t *testing.T) {
	// The classifier is async: the first tool call of a turn is let through
	// while the stance is still unknown. That optimistic pass must NOT pre-empt
	// the first real reading, which may legitimately be "he's just talking".
	h := NewStanceHolder()
	h.Set(StanceUnknown, "classifier unavailable")
	h.MarkWorked("code_agent")
	if latched, _ := h.Latched(); latched {
		t.Fatal("an unknown stance must not latch: the first real reading has to be able to say discuss")
	}
	if !h.Set(StanceDiscuss, "he is talking it through") {
		t.Fatal("the first real reading must apply")
	}
	if got, _ := h.Get(); got != StanceDiscuss {
		t.Fatalf("stance = %q, want discuss", got)
	}
}

func TestEscalateOpensAndLatchesTheTurn(t *testing.T) {
	// plan_approve / plan_resume: the boss's own word. It wins from any prior
	// stance and cannot then be talked back down.
	h := NewStanceHolder()
	h.Set(StanceDiscuss, "asked to talk first")
	h.Escalate("the boss approved the plan")

	if got, why := h.Get(); got != StanceWork || why != "the boss approved the plan" {
		t.Fatalf("Escalate must open the gate, got %q / %q", got, why)
	}
	if h.Set(StanceDiscuss, "chatty follow-up") {
		t.Fatal("an approved turn must not be demoted by a later chatty message")
	}
	if got, _ := h.Get(); got != StanceWork {
		t.Fatalf("stance = %q, want work", got)
	}
}

func TestEscalateReleasesWaiters(t *testing.T) {
	// The loop's first work-tool call blocks on Wait for the first reading.
	// Escalate must unblock it, not just Set — otherwise plan_approve inside a
	// turn whose classifier never answered would stall every following tool.
	h := NewStanceHolder()
	go func() {
		time.Sleep(20 * time.Millisecond)
		h.Escalate("the boss asked to continue an approved plan")
	}()
	st, _ := h.Wait(context.Background(), time.Second)
	if st != StanceWork {
		t.Fatalf("Wait returned %q, want work", st)
	}
}

func TestNilHolderIsInert(t *testing.T) {
	var h *StanceHolder
	if h.Set(StanceWork, "x") {
		t.Fatal("nil holder must report nothing applied")
	}
	h.Escalate("x")
	h.MarkWorked("x")
	if latched, _ := h.Latched(); latched {
		t.Fatal("nil holder must not report latched")
	}
	if n, _ := h.RefusedDemotions(); n != 0 {
		t.Fatal("nil holder must not report refusals")
	}
}
