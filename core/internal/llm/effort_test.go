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

// TestBuildResponsesRequestEffort proves the GATE (modelSupportsReasoning), not
// the caller, decides whether effort is sent — the verifier's intent test for
// the OAuth (Codex) path the boss's brain runs on.
func TestBuildResponsesRequestEffort(t *testing.T) {
	// reasoning model + level -> reasoning.effort is set.
	body := buildResponsesRequest("gpt-5-codex", "", "", "high", nil, nil)
	r, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("reasoning block missing on a reasoning model")
	}
	if r["effort"] != "high" {
		t.Fatalf("effort not applied: got %v", r["effort"])
	}
	// reasoning model + "" -> effort OMITTED (model default; never a literal).
	body = buildResponsesRequest("gpt-5-codex", "", "", "", nil, nil)
	r, _ = body["reasoning"].(map[string]any)
	if _, has := r["effort"]; has {
		t.Fatal("effort must be omitted when level is empty (model default)")
	}
	// non-reasoning model -> no reasoning block at all, regardless of effort.
	body = buildResponsesRequest("gpt-4o", "", "", "high", nil, nil)
	if _, has := body["reasoning"]; has {
		t.Fatal("non-reasoning model must never receive reasoning/effort")
	}
}

// TestEffortToBudget proves the Anthropic ladder: none disables, env budget is a
// ceiling the ladder scales within (auto-routing never exceeds a boss-set value).
func TestEffortToBudget(t *testing.T) {
	if b := effortToBudget(EffortNone, 16384); b != 0 {
		t.Fatalf("none must disable thinking, got %d", b)
	}
	// xhigh within a configured ceiling == the ceiling, never above.
	if b := effortToBudget(EffortXHigh, 16384); b != 16384 {
		t.Fatalf("xhigh should cap at the env ceiling 16384, got %d", b)
	}
	// a mid level scales below the ceiling and respects the 1024 minimum.
	if b := effortToBudget(EffortLow, 16384); b < 1024 || b >= 16384 {
		t.Fatalf("low should scale within (1024..ceiling), got %d", b)
	}
	// no configured ceiling -> default ceiling lets the feature still work.
	if b := effortToBudget(EffortHigh, 0); b < 1024 {
		t.Fatalf("high with no env budget should still enable thinking, got %d", b)
	}
}
