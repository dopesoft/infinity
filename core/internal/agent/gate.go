package agent

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ToolGate decides whether a tool call may execute. Implementations sit
// between the loop and the registry — high-risk calls (e.g. Claude-Code
// shell-outs from a phone in an Uber) divert into the Trust queue instead of
// running blind. Returning Allow=false with WaitForApproval=true makes the
// loop block on `WaitForDecision` so the boss can approve inline from the
// tool card and the same tool call runs immediately on approval.
type ToolGate interface {
	// Authorize is called once per tool call before Execute. Returning
	// quickly is important when WaitForApproval is false — the loop is
	// synchronous on the WS stream. When WaitForApproval is true, the
	// loop calls WaitForDecision next and is willing to block there.
	Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) GateDecision

	// WaitForDecision blocks until the named contract reaches a terminal
	// status (approved / denied / consumed / timed-out). Returns true if
	// approved (or already consumed by a previous wait). Honoured only
	// when Authorize returned WaitForApproval=true with a ContractID.
	// Implementations may return early on ctx cancel or when `timeout`
	// elapses; in that case `approved` is false.
	WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (approved bool, reason string)
}

// GateDecision is the outcome of authorize.
//
//   - Allow=true                                  → run the tool immediately.
//   - Allow=false, WaitForApproval=true, Contract → loop blocks on
//     WaitForDecision; on approval the same tool call runs; on denial
//     the synthesized denied output goes back to the model.
//   - Allow=false, WaitForApproval=false          → synthesize Reason as
//     the tool result, never run.
//
// Preview is a short human-readable summary of what the gated action is
// going to do (rendered next to the inline Approve/Deny buttons in
// Studio). Falls back to the model's input JSON when empty.
type GateDecision struct {
	Allow           bool
	Reason          string
	ContractID      string
	WaitForApproval bool
	WaitTimeout     time.Duration
	Preview         string
}

// AllowAll is the default gate. Used when no Trust store is configured.
type AllowAll struct{}

func (AllowAll) Authorize(_ context.Context, _, _, _ string, _ map[string]any) GateDecision {
	return GateDecision{Allow: true}
}

func (AllowAll) WaitForDecision(_ context.Context, _ string, _ time.Duration) (bool, string) {
	// No gate, no wait. Should never be called.
	return true, ""
}

// IsClaudeCodeTool reports whether the tool name belongs to the claude_code
// MCP namespace. Used by gates that want a special policy for shell-outs
// without coupling to the MCP package.
func IsClaudeCodeTool(name string) bool {
	return strings.HasPrefix(name, "claude_code__")
}

// IsGitHubTool reports whether the tool name belongs to the github MCP
// namespace (i.e. github/github-mcp-server). Mirrors IsClaudeCodeTool — used
// by GitHubGate so it can no-op on every other tool call.
func IsGitHubTool(name string) bool {
	return strings.HasPrefix(name, "github__")
}

// IsComposioTool reports whether the tool name belongs to the Composio
// unified gateway. Composio names tools as `composio__<TOOLKIT>_<VERB>`
// (e.g. composio__GITHUB_CREATE_ISSUE, composio__GMAIL_SEND_EMAIL), so a
// single gate can apply pattern-based policy across every toolkit Composio
// fronts. See proactive.ComposioGate.
func IsComposioTool(name string) bool {
	return strings.HasPrefix(name, "composio__")
}

// IsBridgeTool reports whether a tool is one of the generic bridge
// primitives (fs_*, bash_run, git_*) OR a higher-level bridge-backed
// orchestration tool like project_create. These all route per-session
// via the bridge.Router and operate on either the Mac filesystem (when
// Mac is the active bridge) or the Railway workspace volume (when
// Cloud is active). They run as Jarvis's direct file/bash/git verbs —
// no sub-agent — so the Trust queue is the only safety layer; gate
// them just like the claude_code__* mutators.
//
// project_create gets atomically gated at this level so the boss
// approves once per project bootstrap; the internal bash + GitHub-API
// calls don't re-prompt.
func IsBridgeTool(name string) bool {
	switch name {
	case "fs_read", "fs_ls", "fs_save", "fs_edit",
		"bash_run",
		"git_status", "git_diff", "git_stage", "git_commit",
		"git_push", "git_pull",
		"project_create":
		return true
	}
	return false
}

// GateChain composes multiple ToolGate implementations into one. Each call
// to Authorize walks the chain in order: the first gate that returns a
// non-allow decision wins. Gates that don't recognise the tool name MUST
// return Allow=true so subsequent gates get a chance.
//
// WaitForDecision routing: every gate that issues a ContractID must be
// recorded so we can dispatch the wait back to it. We track ownership in
// an in-memory map keyed by ContractID. The map only needs to survive
// across one (Authorize → WaitForDecision) cycle inside the same agent
// turn — a Railway restart kills the loop anyway, so persistence is moot.
//
// This is the seam that turns Infinity from "claude_code Trust queue" into
// a per-MCP gate system. Adding Gmail/Slack/Linear later just means adding
// another gate to the chain.
type GateChain struct {
	gates []ToolGate
	mu    sync.Mutex
	owner map[string]ToolGate // contractID → gate that queued it
}

// NewGateChain returns a chain over the given gates. Nil gates are skipped
// so callers can build the slice conditionally without nil-checks.
func NewGateChain(gates ...ToolGate) *GateChain {
	out := make([]ToolGate, 0, len(gates))
	for _, g := range gates {
		if g == nil {
			continue
		}
		out = append(out, g)
	}
	return &GateChain{gates: out, owner: make(map[string]ToolGate)}
}

func (c *GateChain) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) GateDecision {
	if c == nil {
		return GateDecision{Allow: true}
	}
	for _, g := range c.gates {
		d := g.Authorize(ctx, sessionID, project, toolName, input)
		if d.Allow {
			continue
		}
		if d.ContractID != "" {
			c.mu.Lock()
			c.owner[d.ContractID] = g
			c.mu.Unlock()
		}
		return d
	}
	return GateDecision{Allow: true}
}

func (c *GateChain) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if c == nil || contractID == "" {
		return false, "no contract id"
	}
	c.mu.Lock()
	owner := c.owner[contractID]
	c.mu.Unlock()
	if owner == nil {
		// Lost ownership (process restart between Authorize and wait, or
		// chain replaced). Fall back to the first gate — every gate shares
		// the same TrustStore today, so polling against any of them reads
		// the right row.
		if len(c.gates) == 0 {
			return false, "no gates configured"
		}
		owner = c.gates[0]
	}
	approved, reason := owner.WaitForDecision(ctx, contractID, timeout)
	if approved || reason != "" {
		c.mu.Lock()
		delete(c.owner, contractID)
		c.mu.Unlock()
	}
	return approved, reason
}
