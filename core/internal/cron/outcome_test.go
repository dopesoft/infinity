package cron

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// bridgeUnavailableInSession must return false when pool is nil so unit tests
// that pass nil pools don't panic and the path is safe to call speculatively.
func TestBridgeUnavailableInSession_NilPool(t *testing.T) {
	if bridgeUnavailableInSession(context.Background(), nil, "any-session-id") {
		t.Fatal("expected false with nil pool")
	}
	if bridgeUnavailableInSession(context.Background(), nil, "") {
		t.Fatal("expected false with empty sessionID")
	}
}

// bridgeBlockedNarrative must not contain any success-indicating words that
// could be mistaken for a confirmed healthy deploy, and must contain the key
// blocking signals the boss needs to understand the situation.
func TestBridgeBlockedNarrative_Content(t *testing.T) {
	successWords := []string{"healthy", "ran it", "clean", "succeeded", "ok", "verified"}
	lowered := strings.ToLower(bridgeBlockedNarrative)
	for _, w := range successWords {
		if strings.Contains(lowered, w) {
			t.Errorf("bridgeBlockedNarrative contains success word %q — must not claim success when workspace is down", w)
		}
	}
	blockingSignals := []string{"unreachable", "unconfirmed", "skipped", "bring a bridge back"}
	for _, sig := range blockingSignals {
		if !strings.Contains(lowered, sig) {
			t.Errorf("bridgeBlockedNarrative missing expected blocking signal %q", sig)
		}
	}
}

// planIncomplete must return true when any step has status "failed", not just
// when steps are pending or in_progress. A nightly self-improve run where the
// build step fails but subsequent steps are skipped/done must classify as
// stopped_early, NOT did_work.
func TestPlanIncomplete_FailedStepIsIncomplete(t *testing.T) {
	p := &plan.Plan{
		Steps: []plan.Step{
			{Status: plan.StepFailed},  // build failed
			{Status: plan.StepSkipped}, // push never ran
		},
	}
	if !planIncomplete(p) {
		t.Fatal("planIncomplete should return true when any step is failed")
	}
	// A plan with only done/skipped steps should not be flagged incomplete.
	p2 := &plan.Plan{
		Steps: []plan.Step{
			{Status: plan.StepDone},
			{Status: plan.StepSkipped},
		},
	}
	if planIncomplete(p2) {
		t.Fatal("planIncomplete should return false when all steps are done or skipped")
	}
}

// A run that abandoned plan steps or had errored turns must never classify as
// "did_work" — the boss saw "DONE" chips on self-improve runs whose own report
// said "I did not actually run the verification".
func TestClassifyOutcome_Precedence(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		summary RunSummary
		execErr error
		want    Outcome
	}{
		{"exec error wins", RunSummary{}, errors.New("boom"), OutcomeFailed},
		{"executor-declared outcome honoured", RunSummary{Meta: map[string]any{"outcome": "nothing_needed"}}, nil, OutcomeNothingNeeded},
		{"zero turns reads nothing_needed", RunSummary{Meta: map[string]any{"turns": 0}}, nil, OutcomeNothingNeeded},
		{"abandoned steps read stopped_early", RunSummary{Meta: map[string]any{"turns": 1, "abandoned_steps": 2}}, nil, OutcomeStoppedEarly},
		{"errored turns read stopped_early", RunSummary{Meta: map[string]any{"turns": 3, "failures": 1}}, nil, OutcomeStoppedEarly},
		{"clean run reads did_work", RunSummary{Meta: map[string]any{"turns": 2, "failures": 0}}, nil, OutcomeDidWork},
	}
	for _, c := range cases {
		// nil pool: the trust/plan DB checks are skipped; the meta-driven
		// precedence under test is pure signal.
		if got := classifyOutcome(ctx, nil, "", c.summary, c.execErr); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
