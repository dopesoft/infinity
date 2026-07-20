package tools

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/turnctx"
	"github.com/google/uuid"
)

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
type sessionIDContextKey struct{}

// WithAutonomous / IsAutonomous forward to the leaf turnctx package so the SAME
// marker can be read from packages that can't import tools (e.g. memory's
// PlanProvider, which must not show a fresh interactive session another
// session's plan). Signatures unchanged so every existing caller keeps working.
func WithAutonomous(ctx context.Context) context.Context { return turnctx.WithAutonomous(ctx) }
func IsAutonomous(ctx context.Context) bool              { return turnctx.IsAutonomous(ctx) }

// WithSessionID stashes the current session's ID in ctx. Used by tools
// that need to query session-scoped state (e.g., the bridge tools need
// mem_sessions.bridge_preference). The loop should call this before
// invoking any tool.
func WithSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDContextKey{}, id)
}

// SessionIDFromContext returns the current session ID, or "" when
// unset (CLI invocations, tests).
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(sessionIDContextKey{}).(string)
	return v
}

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

// isPlainSessionID reports whether sid is a bare UUID — a real chat session,
// not a synthetic sub-agent id like "background:<uuid>", "delegate:<uuid>",
// or "peer:<slug>". Only plain UUIDs are safe for UUID-typed DB columns such
// as mem_artifacts.source_session_id; synthetic ids carry a prefix that
// makes PostgreSQL throw SQLSTATE 22P02.
func isPlainSessionID(sid string) bool {
	_, err := uuid.Parse(sid)
	return err == nil
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
