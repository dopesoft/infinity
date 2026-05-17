package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// defaultApprovalTTL bounds how long an idle session approval lasts.
// We use a sliding window - every gate fire renews the expiry - so as
// long as the boss is actively working the approval never lapses. The
// TTL only matters when the session goes quiet (e.g. boss walks away for
// hours). Override with INFINITY_CLAUDE_CODE_APPROVAL_TTL ("8h", "1d", etc.).
const defaultApprovalTTL = 8 * time.Hour

func loadApprovalTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("INFINITY_CLAUDE_CODE_APPROVAL_TTL"))
	if raw == "" {
		return defaultApprovalTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultApprovalTTL
	}
	return d
}

// ClaudeCodeGate is the agent.ToolGate implementation that routes high-risk
// claude_code__* tool calls through the Trust queue.
//
// Policy (overridable via env):
//
//   • claude_code__bash, claude_code__write, claude_code__edit  → high-risk → queue
//   • claude_code__read, claude_code__ls, claude_code__grep,
//     claude_code__glob                                          → low-risk → allow
//   • everything else                                            → allow
//
// Override:
//   $INFINITY_CLAUDE_CODE_AUTOAPPROVE   comma list of tool suffixes that always allow
//                                       (e.g. "bash,edit" if you trust the box)
//   $INFINITY_CLAUDE_CODE_BLOCK         comma list of tool suffixes to always queue
//                                       (default = "bash,write,edit")
type ClaudeCodeGate struct {
	trust      *TrustStore
	autoAllow  map[string]struct{}
	alwaysGate map[string]struct{}
	gitReadOK  map[string]struct{} // git subcommands that bypass bash-gating
	ttl        time.Duration
}

func NewClaudeCodeGate(trust *TrustStore) *ClaudeCodeGate {
	gitRead := parseToolSet(envOr("INFINITY_CANVAS_GIT_READ_AUTOAPPROVE",
		"status,diff,log,show,branch,remote,fetch,rev-parse,ls-files,ls-tree,blame,config"))
	return &ClaudeCodeGate{
		trust:      trust,
		autoAllow:  parseToolSet(os.Getenv("INFINITY_CLAUDE_CODE_AUTOAPPROVE")),
		alwaysGate: parseToolSet(envOr("INFINITY_CLAUDE_CODE_BLOCK", "bash,write,edit")),
		gitReadOK:  gitRead,
		ttl:        loadApprovalTTL(),
	}
}

// isReadOnlyGit reports whether a bash invocation is a safe, read-only git
// query (`git status`, `git diff`, `git log`…). Canvas's IDE-style left pane
// polls these constantly; gating every call would drown the boss in Trust
// prompts for actions that can't change disk or remote state.
//
// Rules to qualify:
//   - input has a "command" string starting with "git "
//   - the subcommand is in gitReadOK
//   - no shell metacharacters that could append a write (`;`, `&&`, `||`,
//     `|`, backticks, `$(`). The check is conservative; in particular it
//     rejects any redirection (`>`, `<`) and any newline.
func (g *ClaudeCodeGate) isReadOnlyGit(input map[string]any) bool {
	if g == nil || len(g.gitReadOK) == 0 {
		return false
	}
	raw, _ := input["command"].(string)
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return false
	}
	for _, bad := range []string{";", "&&", "||", "|", "`", "$(", ">", "<", "\n", "\r"} {
		if strings.Contains(cmd, bad) {
			return false
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) < 2 || fields[0] != "git" {
		return false
	}
	sub := strings.ToLower(fields[1])
	_, ok := g.gitReadOK[sub]
	return ok
}

func parseToolSet(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Authorize implements agent.ToolGate. Only acts on claude_code__* tool
// names; everything else passes through.
func (g *ClaudeCodeGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsClaudeCodeTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	suffix := strings.TrimPrefix(toolName, "claude_code__")
	suffix = strings.ToLower(suffix)

	if _, ok := g.autoAllow[suffix]; ok {
		return agent.GateDecision{Allow: true}
	}
	if _, ok := g.alwaysGate[suffix]; !ok {
		// Not in the block list and not explicitly auto-allowed: default allow.
		return agent.GateDecision{Allow: true}
	}

	// Canvas surface: claude_code__bash gets a narrow read-only git allow-list.
	// Read-only verbs cannot change disk or remote state, so gating them just
	// blocks the IDE-style status polling and frustrates the boss without any
	// safety win. Mutations (commit/push/pull/reset/checkout) still fall
	// through to the normal Trust queue below.
	if suffix == "bash" && g.isReadOnlyGit(input) {
		return agent.GateDecision{Allow: true}
	}

	// All approval state lives in mem_trust_contracts - no in-memory cache
	// to lose on a Railway redeploy. Two checks against the durable store:
	//
	//   1. HasRecentApprovalForTool - has the boss ever approved THIS
	//      (session, tool) in the last TTL window? If yes → allow. This
	//      handles both first-call-after-approval AND every follow-up
	//      call in the same session for the duration of the window.
	//
	//   2. ConsumeApprovedForTool - fold the first 'approved' row over
	//      to 'consumed' for audit clarity. Subsequent calls hit (1)
	//      because 'consumed' is also acceptable evidence of approval.
	//
	// Deploy semantics: the rows survive in Postgres, so after a deploy
	// the gate happily allows tools the boss previously approved without
	// re-prompting. Old in-memory map is gone.
	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("ClaudeCodeGate: approval lookup error: %v", err)
			// Fall through to queueing - fail closed.
		} else if hasApproval {
			// Best-effort fold approved→consumed so the audit view shows
			// when the approval was first acted on. Idempotent: if the
			// row is already 'consumed' this no-ops.
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			log.Printf("ClaudeCodeGate: %s allowed via prior approval (durable, %s window)",
				toolName, g.ttl)
			return agent.GateDecision{Allow: true}
		}
	}

	// No standing approval. Queue a fresh contract and block the loop
	// on its resolution. The inline Approve/Deny buttons in Studio's
	// tool card POST to /api/trust-contracts/:id/decide →
	// WaitForDecision returns → the agent runs the same tool call
	// inline. No tab switch, no "retry" message.

	// Queue for approval.
	if g.trust == nil {
		log.Printf("ClaudeCodeGate: trust store nil, refusing %s", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run claude_code__" + suffix + " unattended",
		}
	}

	preview := buildPreview(toolName, input)
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     fmt.Sprintf("Run %s on home Mac", toolName),
		RiskLevel: "high",
		Source:    "claude_code_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: "Claude Code requested a write/edit/exec on the home Mac. Gated for safety because " +
			"INFINITY_CLAUDE_CODE_BLOCK includes this verb.",
		Preview: preview,
	})
	if err != nil {
		log.Printf("ClaudeCodeGate: %s queue err=%v", toolName, err)
		return agent.GateDecision{
			Allow:  false,
			Reason: "could not queue trust contract: " + err.Error(),
		}
	}
	if id == "" {
		// Queue swallows pool=nil with a silent ("", nil). That used to leave
		// the model telling the user "queued in the Trust tab" while nothing
		// actually landed. Fail loud instead - the agent's tool output and
		// the boss's logs both surface the real story.
		log.Printf("ClaudeCodeGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store unavailable; row was NOT persisted - do not tell the boss it was queued",
		}
	}
	log.Printf("ClaudeCodeGate: %s queued as contract=%s (loop will wait)", toolName, id)
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     15 * time.Minute,
		Preview:         preview,
	}
}

// WaitForDecision implements agent.ToolGate. Polls the contract's status
// every second until it leaves "pending", or the timeout / ctx fires.
// Returns (approved, reason). On approval we also seed the session-wide
// approval cache so follow-up calls in the same turn don't bounce.
func (g *ClaudeCodeGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if g == nil || g.trust == nil || contractID == "" {
		return false, "trust store not configured"
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			// Returns context.DeadlineExceeded on timeout, context.Canceled
			// when the parent (turn) ctx fires.
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return false, "timed out waiting for approval (" + timeout.String() + ")"
			}
			return false, "session ended before approval"
		case <-tick.C:
			status, sessionID, toolName, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue // transient db error, keep polling
			}
			switch status {
			case "approved":
				log.Printf("ClaudeCodeGate: contract %s approved", contractID)
				// Flip the row to 'consumed' for audit. The gate's
				// HasRecentApprovalForTool will continue to see this
				// row as evidence of approval for the whole TTL
				// window, so further calls in the same session
				// auto-allow without re-queueing.
				_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed by the boss (treat as deny for this run)"
			case "consumed":
				// Already used by another flow - accept it.
				return true, ""
			default:
				// "pending" - keep waiting.
				continue
			}
		}
	}
}

func buildPreview(toolName string, input map[string]any) string {
	b, err := json.MarshalIndent(map[string]any{
		"tool":  toolName,
		"input": input,
	}, "", "  ")
	if err != nil {
		return toolName
	}
	if len(b) > 4096 {
		b = append(b[:4093], []byte("...")...)
	}
	return string(b)
}
