// Package turnctx holds the tiny, dependency-free context markers that describe
// the CURRENT turn and need to be read from more than one package (tools AND
// memory). It's a leaf package (imports only stdlib context) so anything can
// import it without a cycle — tools re-exports WithAutonomous/IsAutonomous as
// forwarders for backward compatibility, and memory reads IsAutonomous directly
// to keep a fresh interactive session from being shown another session's plan.
package turnctx

import "context"

type autonomousContextKey struct{}

// WithAutonomous marks ctx as belonging to an AUTONOMOUS turn — one the boss is
// not actively driving (cron fire, heartbeat scan, delegated/team sub-agent).
// The agent loop sets this whenever Run is invoked with a nil steer channel.
func WithAutonomous(ctx context.Context) context.Context {
	return context.WithValue(ctx, autonomousContextKey{}, true)
}

// IsAutonomous reports whether the current turn is autonomous. Defaults to false
// so any path that forgot to mark the context is treated as INTERACTIVE — the
// safe default for capability, and the correct default for session-scoping (an
// unmarked turn never reaches across sessions for a plan).
func IsAutonomous(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(autonomousContextKey{}).(bool)
	return v
}
