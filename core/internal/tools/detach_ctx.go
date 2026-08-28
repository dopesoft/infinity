package tools

import (
	"context"
	"sync"
)

// The DETACH signal: "stop waiting for me, but keep working."
//
// 2026-08-28. Until now the loop had exactly one way to react to the boss
// speaking mid-tool: cancel the tool's context. For a tool whose work lives
// OUTSIDE this process - code_agent's detached `claude -p` on the Mac - that
// meant every question ("how's it going?") killed a real build.
//
// A cancel and a detach are different orders and now have different wires. A
// cancel still means "tear it down". A detach means "the turn is over for
// you; return what you can say now and let the work finish on its own". The
// loop fires it; a tool that outlives the turn (tools.SteerDetachable) reads
// it. Tools that don't care never see it - DetachRequested returns nil and
// their select never has that case.
//
// Generic by construction: the loop knows nothing about code_agent, only that
// a tool declared itself detachable, and the signal travels on the context
// every tool already receives.

type detachContextKey struct{}

// WithDetachSignal returns a context carrying a detach channel, plus the
// function that fires it. Firing is idempotent and never blocks, so the loop
// can call it from any path.
func WithDetachSignal(ctx context.Context) (context.Context, func()) {
	ch := make(chan struct{})
	var once sync.Once
	return context.WithValue(ctx, detachContextKey{}, (<-chan struct{})(ch)), func() {
		once.Do(func() { close(ch) })
	}
}

// DetachRequested returns the detach channel for this tool call, or nil when
// the caller did not offer one (a cron, a delegate, a CLI invocation). A nil
// channel in a select blocks forever, which is exactly the old behaviour.
func DetachRequested(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	ch, _ := ctx.Value(detachContextKey{}).(<-chan struct{})
	return ch
}
