package agent

import (
	"context"
	"errors"
)

// turn_budget.go - why a turn's context was cancelled, when it was not the boss.
//
// The server bounds a live turn two ways (server/turn_budget.go): a STALL
// budget, cancelled when the brain has produced nothing for a while during a
// phase where silence means it is wedged, and a CEILING, the absolute wall
// clock no turn may outlive. Both cancel the turn context with one of these
// causes so the loop can tell them from the Stop button (a plain cancel) and
// close the turn honestly: a note the boss can act on, a stop reason /logs can
// filter, and the partial reply kept.

// ErrTurnStalled is the cancel cause when the brain went quiet for longer than
// the stall budget while thinking or streaming.
var ErrTurnStalled = errors.New("turn stalled: no output from the brain")

// ErrTurnCeiling is the cancel cause when the turn hit the absolute ceiling.
var ErrTurnCeiling = errors.New("turn hit its time ceiling")

// TurnBudgetCause reports which budget, if any, ended the turn. A context
// cancelled by the Stop button (or not cancelled at all) reports neither.
func TurnBudgetCause(ctx context.Context) (stalled, ceiling bool) {
	if ctx == nil || ctx.Err() == nil {
		return false, false
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrTurnStalled) {
		return true, false
	}
	// A plain deadline (a caller that still uses WithTimeout) is a ceiling.
	if errors.Is(cause, ErrTurnCeiling) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, true
	}
	return false, false
}
