package effort

import (
	"context"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

func resolve(in Inputs) Decision {
	return NewRouter(nil).Resolve(context.Background(), in)
}

// boss pin is never overridden, even with every escalating signal hot.
// Intent: the boss's explicit choice is sacred.
func TestBossPinnedWins(t *testing.T) {
	d := resolve(Inputs{
		Pinned: "low", Supported: true, Coding: true,
		PriorSurprise: 0.99, ToolErrorRate: 0.9, CallRate: 100, Ceiling: 50, ContextFill: 0.99,
	})
	if d.Level != llm.EffortLow || d.Source != "boss_pinned" {
		t.Fatalf("got %+v, want low/boss_pinned", d)
	}
}

// a pinned level on a non-reasoning model is dropped to omit, never honored by
// swapping models. Intent: respect_settings + never request effort a model drops.
func TestBossPinDroppedWhenUnsupported(t *testing.T) {
	d := resolve(Inputs{Pinned: "high", Supported: false})
	if d.Level != "" || d.Source != "unsupported" {
		t.Fatalf("got %+v, want \"\"/unsupported", d)
	}
}

// non-reasoning model with no pin omits. Intent: capability clamp.
func TestUnsupportedOmits(t *testing.T) {
	d := resolve(Inputs{Supported: true, Coding: true})
	if d.Level != llm.EffortMedium {
		t.Fatalf("coding on a reasoning model should floor at medium, got %+v", d)
	}
	d = resolve(Inputs{Supported: false, Coding: true})
	if d.Level != "" || d.Source != "unsupported" {
		t.Fatalf("got %+v, want \"\"/unsupported", d)
	}
}

// coding floors at medium even with everything else neutral.
// Intent: real work gets real thought.
func TestCodingFloorsMedium(t *testing.T) {
	d := resolve(Inputs{Supported: true, Coding: true})
	if d.Level != llm.EffortMedium || d.Source != "coding_floor" {
		t.Fatalf("got %+v, want medium/coding_floor", d)
	}
}

// coding + high surprise reaches xhigh (the genuinely stuck heavy turn).
func TestCodingPlusSurpriseIsXHigh(t *testing.T) {
	d := resolve(Inputs{Supported: true, Coding: true, PriorSurprise: 0.9})
	if d.Level != llm.EffortXHigh {
		t.Fatalf("got %+v, want xhigh", d)
	}
}

// non-coding turn caps at high no matter how many signals stack (xhigh reserved).
func TestNonCodingCapsAtHigh(t *testing.T) {
	d := resolve(Inputs{
		Supported: true, PriorSurprise: 0.9, ToolErrorRate: 0.9, ContextFill: 0.9,
		CallRate: 100, Ceiling: 50,
	})
	if d.Level != llm.EffortHigh {
		t.Fatalf("got %+v, want high (xhigh reserved for coding)", d)
	}
}

// all deterministic signals neutral -> gauge is the tiebreak, and only then.
func TestGaugeFallbackOnlyWhenNeutral(t *testing.T) {
	if d := resolve(Inputs{Supported: true, Gauge: GaugeDeep}); d.Level != llm.EffortHigh || d.Source != "gauge_deep" {
		t.Fatalf("deep gauge: got %+v, want high/gauge_deep", d)
	}
	if d := resolve(Inputs{Supported: true, Gauge: GaugeStandard}); d.Level != llm.EffortLow || d.Source != "gauge_standard" {
		t.Fatalf("standard gauge: got %+v, want low/gauge_standard", d)
	}
	if d := resolve(Inputs{Supported: true, Gauge: GaugeGlance}); d.Level != "" || d.Source != "gauge_glance" {
		t.Fatalf("glance gauge: got %+v, want \"\"/gauge_glance", d)
	}
	// a deterministic signal beats the gauge: surprise present -> NOT gauge.
	if d := resolve(Inputs{Supported: true, PriorSurprise: 0.9, Gauge: GaugeGlance}); d.Source == "gauge_glance" {
		t.Fatalf("deterministic signal must outrank the gauge, got %+v", d)
	}
}

// neutral everything -> floor (omit), never a literal "none". Intent: zero cost
// change out of the box.
func TestNeutralIsOmit(t *testing.T) {
	d := resolve(Inputs{Supported: true})
	if d.Level != "" {
		t.Fatalf("neutral turn must omit (\"\"), got %+v", d)
	}
}

// identical inputs -> identical output across runs.
func TestIdempotent(t *testing.T) {
	in := Inputs{Supported: true, Coding: true, PriorSurprise: 0.9, Gauge: GaugeDeep}
	first := resolve(in)
	for i := 0; i < 5; i++ {
		if got := resolve(in); got.Level != first.Level || got.Source != first.Source {
			t.Fatalf("non-idempotent: %+v vs %+v", got, first)
		}
	}
}
