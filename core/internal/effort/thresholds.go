// Package effort is the deterministic per-turn reasoning-effort router for
// steal C. It NEVER touches the model id - it only decides how much compute the
// already-resolved model spends this turn (GPT none|low|medium|high|xhigh /
// Anthropic thinking budget). Every input it consumes is a value already
// computed elsewhere (prediction surprise, loop-gate call rate, the gauge), so
// the decision is arithmetic - code answers, not an LLM (Rule #5). The single
// genuine NL judgment ("how hard is this request?") is the Gauge, which already
// runs async and is passed in as a string.
package effort

// Thresholds are the escalation bars. They ship as code defaults and may be
// overridden at runtime from infinity_meta key setting.effort_thresholds (wired
// in serve.go), with the nightly self-tuner nudging them within clamps. An
// empty/corrupt override falls back to these defaults.
type Thresholds struct {
	// SurpriseHi: a prior-turn prediction surprise at/above this escalates.
	// 0.85 mirrors the real curiosity high-surprise bar (curiosity.go:290);
	// it is NOT a shared const yet, so we carry the value here honestly.
	SurpriseHi float64
	// ErrRateHi: today's tool-error-rate above this escalates.
	ErrRateHi float64
	// CallRateFrac: calls-in-window / ceiling at/above this escalates (the
	// session is working hard and may be stuck).
	CallRateFrac float64
	// CtxFrac: context-window fill above this escalates (long, dense turn).
	CtxFrac float64
}

// DefaultThresholds are the shipped defaults. Conservative: ordinary chat stays
// at the floor (omit -> model default), escalation needs a concrete signal.
func DefaultThresholds() Thresholds {
	return Thresholds{
		SurpriseHi:   0.85,
		ErrRateHi:    0.25,
		CallRateFrac: 0.5,
		CtxFrac:      0.6,
	}
}

// sane clamps the loaded/tuned thresholds into a safe range so a bad override or
// a runaway nightly nudge can never disable escalation or escalate on noise.
func (t Thresholds) sane() Thresholds {
	clamp := func(v, lo, hi, def float64) float64 {
		if v <= 0 || v != v { // <=0 or NaN -> default
			return def
		}
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	d := DefaultThresholds()
	return Thresholds{
		SurpriseHi:   clamp(t.SurpriseHi, 0.5, 0.99, d.SurpriseHi),
		ErrRateHi:    clamp(t.ErrRateHi, 0.05, 0.9, d.ErrRateHi),
		CallRateFrac: clamp(t.CallRateFrac, 0.25, 0.95, d.CallRateFrac),
		CtxFrac:      clamp(t.CtxFrac, 0.3, 0.95, d.CtxFrac),
	}
}
