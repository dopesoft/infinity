package effort

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Gauge tier strings (mirrors core/internal/gauge classifier output). Confirmed
// against gauge.Store when the loop populates Inputs.
const (
	GaugeGlance   = "glance"
	GaugeStandard = "standard"
	GaugeDeep     = "deep"
)

// Inputs are the already-computed per-turn signals. The Loop fills these from
// live state right after model resolution; the router does pure arithmetic on
// them. Keeping the router free of pool/gauge imports avoids any cycle and makes
// every decision unit-testable.
type Inputs struct {
	Pinned        string  // boss override: "" | "auto" | a level. A real level wins outright.
	Supported     bool    // modelSupportsReasoning(model): false -> omit, never swap models.
	Coding        bool    // project-set OR a code-write/heavy-workflow tool fired this session.
	PriorSurprise float64 // max recent prediction surprise for this session (0..1).
	ToolErrorRate float64 // today's mem_agent_metrics tool-error rate (0..1).
	CallRate      int     // loop-gate calls in the rolling window.
	Ceiling       int     // loop-gate ceiling for this session.
	ContextFill   float64 // last prompt tokens / model window (0..1).
	Gauge         string  // "glance"|"standard"|"deep"|"" (the async NL difficulty read).
}

// Decision is the router output: the level to apply, the dominant source for
// the audit trail, and every reason that fired (for the nightly self-tuner and
// observability). Source is never empty.
type Decision struct {
	Level   llm.Effort
	Source  string
	Reasons []string
}

// Router resolves a per-turn effort level. thresh loads the (possibly tuned)
// thresholds; nil uses code defaults.
type Router struct {
	thresh func(context.Context) Thresholds
}

// NewRouter builds a router. Pass nil for thresh to use DefaultThresholds.
func NewRouter(thresh func(context.Context) Thresholds) *Router {
	return &Router{thresh: thresh}
}

// level order: none < low < medium < high < xhigh.
var ladder = []llm.Effort{llm.EffortNone, llm.EffortLow, llm.EffortMedium, llm.EffortHigh, llm.EffortXHigh}

func levelAt(i int) llm.Effort {
	if i < 0 {
		i = 0
	}
	if i >= len(ladder) {
		i = len(ladder) - 1
	}
	return ladder[i]
}

// Resolve is deterministic and idempotent: identical Inputs -> identical
// Decision. It fails open (returns level "" / source "error" is reserved for the
// caller's panic-guard; Resolve itself never panics on these inputs).
func (r *Router) Resolve(ctx context.Context, in Inputs) Decision {
	// 1. Boss pin wins, always - an explicit choice is never overridden.
	if lv := llm.Effort(in.Pinned); lv.Valid() {
		// Even a pin must respect capability: a level on a non-reasoning model
		// is dropped to omit (never honored by swapping models).
		if !in.Supported {
			return Decision{Level: "", Source: "unsupported", Reasons: []string{"boss_pinned_dropped"}}
		}
		return Decision{Level: lv, Source: "boss_pinned", Reasons: []string{"boss_pinned"}}
	}

	// 2. Capability clamp - model can't reason: omit, keep its default.
	if !in.Supported {
		return Decision{Level: "", Source: "unsupported"}
	}

	t := DefaultThresholds()
	if r != nil && r.thresh != nil {
		t = r.thresh(ctx)
	}
	t = t.sane()

	// 3. Signals (deterministic; every value pre-computed elsewhere).
	surpriseHit := in.PriorSurprise >= t.SurpriseHi
	callFrac := 0.0
	if in.Ceiling > 0 {
		callFrac = float64(in.CallRate) / float64(in.Ceiling)
	}
	callHit := in.Ceiling > 0 && callFrac >= t.CallRateFrac
	errHit := in.ToolErrorRate > t.ErrRateHi
	ctxHit := in.ContextFill > t.CtxFrac

	// reasons in priority order; the first becomes the dominant source.
	var reasons []string
	if surpriseHit {
		reasons = append(reasons, "high_surprise")
	}
	if callHit {
		reasons = append(reasons, "call_rate")
	}
	if errHit {
		reasons = append(reasons, "tool_error_rate")
	}
	if ctxHit {
		reasons = append(reasons, "context_fill")
	}

	// 4. xhigh reservation is SUFFICIENT: a coding turn that is also stuck
	// (a very-wrong prediction OR thrashing call-rate) gets max compute. xhigh
	// is never reachable on a non-coding turn.
	if in.Coding && (surpriseHit || callHit) {
		src := "call_rate"
		if surpriseHit {
			src = "high_surprise"
		}
		return Decision{Level: llm.EffortXHigh, Source: src, Reasons: reasons}
	}

	// 5. Points: coding floors at medium; each hot signal +1; capped at high
	// (xhigh handled above). none(0) < low(1) < medium(2) < high(3).
	points := 0
	source := "default"
	if in.Coding {
		points = 2 // real work gets real thought
		source = "coding_floor"
	}
	points += len(reasons)
	if points > 3 {
		points = 3
	}
	if len(reasons) > 0 {
		source = reasons[0]
	}

	// 6. Gauge fallback: only when fully neutral (not coding, no signals). The
	// gauge already ran async - no new model call here.
	if points == 0 && len(reasons) == 0 {
		switch in.Gauge {
		case GaugeDeep:
			return Decision{Level: llm.EffortHigh, Source: "gauge_deep", Reasons: []string{"gauge_deep"}}
		case GaugeStandard:
			return Decision{Level: llm.EffortLow, Source: "gauge_standard", Reasons: []string{"gauge_standard"}}
		case GaugeGlance:
			// glance -> none == omit; stay at the floor.
			return Decision{Level: "", Source: "gauge_glance", Reasons: []string{"gauge_glance"}}
		}
	}

	// 7. Map; none == omit (surface the floor honestly rather than send "none").
	lvl := levelAt(points)
	if lvl == llm.EffortNone {
		return Decision{Level: "", Source: source, Reasons: reasons}
	}
	return Decision{Level: lvl, Source: source, Reasons: reasons}
}
