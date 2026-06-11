package cron

import (
	"context"
	"errors"
	"testing"
)

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
