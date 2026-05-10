package tools

import "context"

// RegisterDefaults wires native tools (no MCP yet — those come via mcp.go).
// Phase 2 expands this list. Phase 1 starts empty so the chat path works
// even when no API keys for tool providers are configured.
func RegisterDefaults(ctx context.Context, r *Registry) {
	if t, err := NewHTTPFetchFromEnv(); err == nil {
		r.Register(t)
	}
	if t, err := NewWebSearchFromEnv(); err == nil {
		r.Register(t)
	}
	if t, err := NewCodeExecFromEnv(); err == nil {
		r.Register(t)
	}
}
