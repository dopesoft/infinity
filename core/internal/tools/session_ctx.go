package tools

import "context"

// sessionContextKey is the context.WithValue key under which the agent
// loop stashes the current session's ActiveSet before calling a tool.
// Tools that need session-scoped state (load_tools, unload_tools,
// compact_context) pull it via ActiveSetFromContext. Tools that don't
// touch session state ignore the context entirely.
//
// Using context.Value rather than a richer "SessionAwareTool" interface
// keeps the Tool interface a single shape and avoids forcing every tool
// implementation to plumb session arguments it doesn't need.
type sessionContextKey struct{}

// WithActiveSet returns a derived context carrying the ActiveSet pointer
// for the session that's about to execute a tool. The agent loop calls
// this before every tools.Execute so session-aware tools can mutate the
// right session's loaded-tool list.
func WithActiveSet(ctx context.Context, s *ActiveSet) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionContextKey{}, s)
}

// ActiveSetFromContext retrieves the per-session ActiveSet, if any.
// Returns nil when the caller forgot to wrap the context or when a tool
// is invoked outside the loop (e.g. CLI smoke test). Tools must nil-check.
func ActiveSetFromContext(ctx context.Context) *ActiveSet {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(sessionContextKey{}).(*ActiveSet)
	return v
}
