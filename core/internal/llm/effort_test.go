package llm

import (
	"context"
	"testing"
)

// TestEffortValid encodes WHY "" is special: it is the deliberate "omit" sentinel
// that preserves today's behavior, so it must NOT count as a valid level.
func TestEffortValid(t *testing.T) {
	for _, e := range []Effort{EffortNone, EffortLow, EffortMedium, EffortHigh, EffortXHigh} {
		if !e.Valid() {
			t.Errorf("level %q should be valid", e)
		}
	}
	for _, e := range []Effort{Effort(""), Effort("minimal"), Effort("max"), Effort("HIGH")} {
		if e.Valid() {
			t.Errorf("level %q should NOT be valid", e)
		}
	}
}

// TestWithEffortRoundTrip proves a stamped level reads back, and a clean ctx
// returns "". The "" path is the proof that every non-loop caller stays on the
// model default (they never stamp).
func TestWithEffortRoundTrip(t *testing.T) {
	base := context.Background()
	if got := EffortFromContext(base); got != "" {
		t.Fatalf("unstamped ctx should yield \"\", got %q", got)
	}
	ctx := WithEffort(base, EffortHigh)
	if got := EffortFromContext(ctx); got != EffortHigh {
		t.Fatalf("expected high, got %q", got)
	}
}

// TestWithEffortEmptyIsNoOp proves stamping "" or an invalid level does NOT
// mutate the context - the "never silently change cost" guarantee at the seam.
func TestWithEffortEmptyIsNoOp(t *testing.T) {
	base := context.Background()
	if EffortFromContext(WithEffort(base, Effort(""))) != "" {
		t.Error("empty level must be a no-op")
	}
	if EffortFromContext(WithEffort(base, Effort("bogus"))) != "" {
		t.Error("invalid level must be a no-op")
	}
}
