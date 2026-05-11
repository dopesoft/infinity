package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// defaultApprovalTTL bounds how long an idle session approval lasts.
// We use a sliding window — every gate fire renews the expiry — so as
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
	ttl        time.Duration

	// sessionApprovals maps sessionID → toolName → expiry. Populated when
	// a Trust approval is consumed; checked on every gate fire so the LLM
	// can run the same tool repeatedly within one turn (or across turns
	// in the same session) without re-queueing. Sliding window: each hit
	// pushes the expiry forward by `ttl`. Lives in memory only — a core
	// restart clears it, which is the right default (fresh process, fresh
	// approvals; the boss re-approves on a redeploy).
	mu               sync.RWMutex
	sessionApprovals map[string]map[string]time.Time
}

func NewClaudeCodeGate(trust *TrustStore) *ClaudeCodeGate {
	return &ClaudeCodeGate{
		trust:            trust,
		autoAllow:        parseToolSet(os.Getenv("INFINITY_CLAUDE_CODE_AUTOAPPROVE")),
		alwaysGate:       parseToolSet(envOr("INFINITY_CLAUDE_CODE_BLOCK", "bash,write,edit")),
		ttl:              loadApprovalTTL(),
		sessionApprovals: make(map[string]map[string]time.Time),
	}
}

// isSessionApproved reports whether a fresh-enough in-memory approval
// exists for the (session, tool) pair. Expired entries are pruned on
// read. Successful checks also extend the expiry (sliding window) — as
// long as the boss is actively using the tool, the approval never lapses.
func (g *ClaudeCodeGate) isSessionApproved(sessionID, toolName string) bool {
	if g == nil || sessionID == "" {
		return false
	}
	g.mu.RLock()
	tools, ok := g.sessionApprovals[sessionID]
	if !ok {
		g.mu.RUnlock()
		return false
	}
	exp, ok := tools[toolName]
	g.mu.RUnlock()
	if !ok {
		return false
	}
	now := time.Now()
	if now.After(exp) {
		// Lazily clean up the expired entry.
		g.mu.Lock()
		if t, ok := g.sessionApprovals[sessionID]; ok {
			delete(t, toolName)
			if len(t) == 0 {
				delete(g.sessionApprovals, sessionID)
			}
		}
		g.mu.Unlock()
		return false
	}
	// Sliding window: every successful check pushes the expiry forward.
	// Cheap relative to the network round-trip we're about to skip.
	g.mu.Lock()
	if t, ok := g.sessionApprovals[sessionID]; ok {
		t[toolName] = now.Add(g.ttl)
	}
	g.mu.Unlock()
	return true
}

// markSessionApproved adds the (session, tool) → expiry entry so the gate
// auto-allows further calls without round-tripping the DB.
func (g *ClaudeCodeGate) markSessionApproved(sessionID, toolName string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	tools, ok := g.sessionApprovals[sessionID]
	if !ok {
		tools = make(map[string]time.Time)
		g.sessionApprovals[sessionID] = tools
	}
	tools[toolName] = time.Now().Add(g.ttl)
}

// RevokeSession drops every cached approval for a session — used when the
// session ends or when the boss explicitly clears approvals. Defensive.
func (g *ClaudeCodeGate) RevokeSession(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessionApprovals, sessionID)
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

	// Session-wide approval cache: if the boss has already approved THIS
	// tool in THIS session, allow every subsequent invocation for the TTL
	// window. Without this the LLM bounces off the gate on every
	// follow-up call ("now also read X", verification re-runs) and the
	// boss has to re-approve constantly.
	if g.isSessionApproved(sessionID, toolName) {
		return agent.GateDecision{Allow: true}
	}

	// First call of a tool in this session — consult the durable Trust
	// store. A pre-existing approved contract (boss tapped Approve while
	// the previous gate fire was waiting) consumes here and seeds the
	// in-memory cache so further calls skip the DB hit entirely.
	if g.trust != nil {
		consumed, err := g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
		if err != nil {
			log.Printf("ClaudeCodeGate: consume lookup error: %v", err)
			// Fall through to queueing — fail closed.
		} else if consumed {
			log.Printf("ClaudeCodeGate: %s allowed via prior approval (session-wide for %s, sliding)",
				toolName, g.ttl)
			g.markSessionApproved(sessionID, toolName)
			return agent.GateDecision{Allow: true}
		}
	}

	// Queue a fresh contract AND block the loop on its resolution. The
	// inline Approve/Deny buttons in Studio's tool card POST to
	// /api/trust-contracts/:id/decide → WaitForDecision returns →
	// the agent runs the same tool call inline. No tab switch, no
	// "retry" message from the model.

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
		// actually landed. Fail loud instead — the agent's tool output and
		// the boss's logs both surface the real story.
		log.Printf("ClaudeCodeGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store unavailable; row was NOT persisted — do not tell the boss it was queued",
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
				g.markSessionApproved(sessionID, toolName)
				// Consume it so subsequent gate fires don't double-allow
				// the same row — the session cache covers further calls.
				_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed by the boss (treat as deny for this run)"
			case "consumed":
				// Already used by another flow — accept it.
				return true, ""
			default:
				// "pending" — keep waiting.
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
