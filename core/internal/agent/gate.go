package agent

import (
	"context"
	"strings"
)

// ToolGate decides whether a tool call may execute. Implementations sit
// between the loop and the registry — high-risk calls (e.g. Claude-Code
// shell-outs from a phone in an Uber) divert into the Trust queue instead of
// running blind. Returning Allow=false leaves the loop free to surface the
// reason to the model so it can ask the user to approve in Studio.
type ToolGate interface {
	// Authorize is called once per tool call before Execute. Implementations
	// must return quickly — the agent loop is synchronous on the WS stream.
	// Use Queue to write a Trust Contract row asynchronously; the contract
	// id (if any) is surfaced to the model via the gated tool result.
	Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) GateDecision
}

// GateDecision is the outcome of authorize. Allow=true means proceed.
// Allow=false means short-circuit: synthesize a tool result explaining the
// block and skip Execute. ContractID is optional — set when a Trust queue
// row was created.
type GateDecision struct {
	Allow      bool
	Reason     string
	ContractID string
}

// AllowAll is the default gate. Used when no Trust store is configured.
type AllowAll struct{}

func (AllowAll) Authorize(_ context.Context, _, _, _ string, _ map[string]any) GateDecision {
	return GateDecision{Allow: true}
}

// IsClaudeCodeTool reports whether the tool name belongs to the claude_code
// MCP namespace. Used by gates that want a special policy for shell-outs
// without coupling to the MCP package.
func IsClaudeCodeTool(name string) bool {
	return strings.HasPrefix(name, "claude_code__")
}
