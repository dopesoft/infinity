package server

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// turn_budget.go - how long a live turn may run, decided by what it is DOING,
// not by the clock alone.
//
// The old rule was one wall-clock ceiling (15 minutes) on the whole turn. It
// cut off exactly the turns the boss cares most about: a long research turn
// on DeepSeek that was visibly producing frames the whole time got "I hit my
// time budget" at minute fifteen. The clock could not tell working from wedged.
//
// The journal can. It knows the phase and when the last frame arrived, so the
// rule is now in two parts:
//
//   - STALL: while the brain is thinking, streaming or starting up, silence
//     means it is wedged (a provider that stopped answering, a stream that
//     hung). No frame for the stall budget → cancel with ErrTurnStalled.
//     Never while a tool runs or an approval is parked: a twenty-minute build
//     or a decision the boss has not made yet are silent by nature, and the
//     tools carry their own timeouts.
//   - CEILING: the absolute wall clock. Generous, because Stop and the
//     LoopGate are the real backstops against a runaway turn.
//
// Both cancel the turn context WITH A CAUSE, so the loop closes the turn as
// "stalled" / "time_budget" with a note the boss can act on, never as the
// Stop button (a plain cancel) and never as a raw "context deadline exceeded".

const (
	defaultTurnStall   = 10 * time.Minute
	defaultTurnCeiling = 2 * time.Hour
	turnBudgetTick     = 5 * time.Second
)

// turnBudget is one turn's limits. tick is how often the guard looks.
type turnBudget struct {
	stall   time.Duration
	ceiling time.Duration
	tick    time.Duration
}

// turnBudgetFromEnv reads the limits: INFINITY_TURN_STALL (how long the brain
// may be silent mid-thought, default 10m) and INFINITY_TURN_TIMEOUT (the
// ceiling, default 2h). Both Go durations.
func turnBudgetFromEnv() turnBudget {
	b := turnBudget{stall: defaultTurnStall, ceiling: defaultTurnCeiling, tick: turnBudgetTick}
	if d := durationEnv("INFINITY_TURN_STALL"); d > 0 {
		b.stall = d
	}
	if d := durationEnv("INFINITY_TURN_TIMEOUT"); d > 0 {
		b.ceiling = d
	}
	return b
}

func durationEnv(name string) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// stallCounts reports whether silence in this phase means the brain is wedged.
func stallCounts(phase string) bool {
	switch phase {
	case phaseStarting, phaseThinking, phaseStreaming, phaseSteering:
		return true
	}
	return false
}

// guardTurn derives the turn's context and watches the journal: it cancels
// with ErrTurnStalled when the brain has been silent past the stall budget in
// a phase where that means wedged, and with ErrTurnCeiling past the ceiling.
// The watcher stops when the turn ends (journal no longer in flight) or the
// context is done for any other reason.
func guardTurn(parent context.Context, j *turnJournal, b turnBudget) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	if j == nil || b.tick <= 0 {
		return ctx, func() { cancel(nil) }
	}
	go func() {
		t := time.NewTicker(b.tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				a := j.activity()
				if !a.inFlight {
					return
				}
				if b.ceiling > 0 && now.Sub(a.startedAt) > b.ceiling {
					cancel(agent.ErrTurnCeiling)
					return
				}
				if b.stall > 0 && stallCounts(a.phase) && now.Sub(a.activeAt) > b.stall {
					cancel(agent.ErrTurnStalled)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}
