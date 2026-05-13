package proactive

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// composioWriteVerbs is the default pattern allowlist for "this call mutates
// state on a third-party SaaS". Composio names tools as
// <TOOLKIT>_<VERB>_<OBJECT>; we look for verbs that historically rewrite,
// send, post, delete, or otherwise change someone else's system of record.
//
// This is intentionally conservative — false positives (gating a read that
// happens to contain "send" in its name) are recoverable via a single
// boss tap; false negatives (NOT gating a real destructive write) are not.
// Override with $INFINITY_COMPOSIO_BLOCK_VERBS / $INFINITY_COMPOSIO_AUTOAPPROVE_VERBS.
//
// Match is suffix-aware: we tokenise the tool name (everything after the
// toolkit slug) by '_' and check each token against the set. So
// `composio__GITHUB_CREATE_ISSUE` → tokens [CREATE, ISSUE] → CREATE in
// list → gated. `composio__GITHUB_LIST_ISSUES` → tokens [LIST, ISSUES] →
// neither in list → allowed.
const defaultComposioBlockVerbs = "CREATE,UPDATE,DELETE,PATCH,PUT,POST," +
	"SEND,REPLY,FORWARD,COMMENT," +
	"MERGE,CLOSE,REOPEN,ARCHIVE,RESTORE," +
	"PUBLISH,SCHEDULE,CANCEL,REFUND,CHARGE," +
	"INVITE,REMOVE,ASSIGN,UNASSIGN,TRANSFER," +
	"WRITE,EDIT,MODIFY,REVOKE,GRANT,APPROVE,REJECT," +
	"UPLOAD,IMPORT,EXPORT,SYNC"

// ComposioGate is the agent.ToolGate that intercepts composio__* tool calls.
// Composio fronts ~250 SaaS APIs through a single MCP, so per-vendor gating
// (a la ClaudeCodeGate / GitHubGate) doesn't scale — we'd need a new gate
// per toolkit. Instead this gate uses a verb-pattern allowlist that applies
// uniformly: any token in the verb part that matches the block set queues
// the call for boss approval; everything else (GET, LIST, SEARCH, FETCH,
// READ) passes through.
//
// Override:
//
//	$INFINITY_COMPOSIO_BLOCK_VERBS        comma list of verb tokens to gate
//	                                      (overrides defaultComposioBlockVerbs)
//	$INFINITY_COMPOSIO_AUTOAPPROVE_VERBS  comma list that always allow,
//	                                      checked before the block set
//	$INFINITY_COMPOSIO_AUTOAPPROVE_TOOLS  full tool-suffix allowlist (no
//	                                      composio__ prefix), e.g.
//	                                      "GMAIL_SEND_EMAIL" if you trust
//	                                      that flow end-to-end
type ComposioGate struct {
	trust         *TrustStore
	blockVerbs    map[string]struct{}
	allowVerbs    map[string]struct{}
	allowToolFull map[string]struct{}
	ttl           time.Duration
}

func NewComposioGate(trust *TrustStore) *ComposioGate {
	return &ComposioGate{
		trust:         trust,
		blockVerbs:    parseToolSetUpper(envOr("INFINITY_COMPOSIO_BLOCK_VERBS", defaultComposioBlockVerbs)),
		allowVerbs:    parseToolSetUpper(os.Getenv("INFINITY_COMPOSIO_AUTOAPPROVE_VERBS")),
		allowToolFull: parseToolSetUpper(os.Getenv("INFINITY_COMPOSIO_AUTOAPPROVE_TOOLS")),
		ttl:           loadApprovalTTL(),
	}
}

// parseToolSetUpper is parseToolSet but normalises to upper-case so the
// Composio convention (UPPER_SNAKE tool names) matches without per-call
// strings.ToUpper noise.
func parseToolSetUpper(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range strings.Split(raw, ",") {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

// shouldGate inspects the tool-suffix (everything after `composio__`) and
// decides whether the call mutates remote state. Returns the toolkit slug
// for nicer error/log messages.
func (g *ComposioGate) shouldGate(suffix string) (gate bool, toolkit string) {
	up := strings.ToUpper(suffix)
	if _, ok := g.allowToolFull[up]; ok {
		return false, ""
	}
	parts := strings.Split(up, "_")
	if len(parts) == 0 {
		return false, ""
	}
	toolkit = parts[0]
	// Walk tokens after the toolkit slug looking for write verbs. Allow
	// list wins over block list so the boss can punch a hole for a
	// specific flow (e.g. AUTOAPPROVE_VERBS=SEND for a CI bot).
	for _, tok := range parts[1:] {
		if _, ok := g.allowVerbs[tok]; ok {
			return false, toolkit
		}
		if _, ok := g.blockVerbs[tok]; ok {
			return true, toolkit
		}
	}
	return false, toolkit
}

func (g *ComposioGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsComposioTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	suffix := strings.TrimPrefix(toolName, "composio__")
	gate, toolkit := g.shouldGate(suffix)
	if !gate {
		return agent.GateDecision{Allow: true}
	}

	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("ComposioGate: approval lookup error: %v", err)
		} else if hasApproval {
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			log.Printf("ComposioGate: %s allowed via prior approval (durable, %s window)",
				toolName, g.ttl)
			return agent.GateDecision{Allow: true}
		}
	}

	if g.trust == nil {
		log.Printf("ComposioGate: trust store nil, refusing %s", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run composio__" + suffix + " unattended",
		}
	}

	title := fmt.Sprintf("Run %s via Composio", suffix)
	if toolkit != "" {
		title = fmt.Sprintf("Run %s on %s via Composio", suffix, toolkit)
	}

	preview := buildPreview(toolName, input)
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     title,
		RiskLevel: "high",
		Source:    "composio_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"toolkit":    toolkit,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: "Composio requested a state-changing call on " + toolkit + ". Gated because the verb " +
			"pattern matched INFINITY_COMPOSIO_BLOCK_VERBS.",
		Preview: preview,
	})
	if err != nil {
		log.Printf("ComposioGate: %s queue err=%v", toolName, err)
		return agent.GateDecision{Allow: false, Reason: "could not queue trust contract: " + err.Error()}
	}
	if id == "" {
		log.Printf("ComposioGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store unavailable; row was NOT persisted — do not tell the boss it was queued",
		}
	}
	log.Printf("ComposioGate: %s queued as contract=%s (loop will wait)", toolName, id)
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     15 * time.Minute,
		Preview:         preview,
	}
}

// WaitForDecision mirrors the sibling gates; all three poll the same
// mem_trust_contracts table so the underlying logic is identical.
func (g *ComposioGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
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
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return false, "timed out waiting for approval (" + timeout.String() + ")"
			}
			return false, "session ended before approval"
		case <-tick.C:
			status, sessionID, toolName, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue
			}
			switch status {
			case "approved":
				log.Printf("ComposioGate: contract %s approved", contractID)
				_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed by the boss (treat as deny for this run)"
			case "consumed":
				return true, ""
			default:
				continue
			}
		}
	}
}
