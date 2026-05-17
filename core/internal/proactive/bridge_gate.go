package proactive

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// BridgeGate gates the generic bridge_* tools (fs_save, fs_edit,
// bash_run, git_commit, git_push, git_pull) the same way ClaudeCodeGate
// gates claude_code__*. Both bridges (Mac and Cloud) are filesystem-
// modifying surfaces, so a Trust contract is the right safety layer
// regardless of which one ends up serving the call.
//
// Policy:
//
//   • fs_save, fs_edit, bash_run, git_commit, git_push, git_pull,
//     git_stage                               → high-risk → queue
//   • fs_read, fs_ls, git_status, git_diff    → read-only → allow
//
// Override:
//   $INFINITY_BRIDGE_AUTOAPPROVE   comma list of tool names that always allow
//   $INFINITY_BRIDGE_BLOCK         comma list of tool names to always queue
//
// Default block list is conservative because writes propagate to GitHub
// (the Cloud bridge has git push wired against the boss's PAT). One
// blind `git push --force` is enough; gate everything that mutates.
type BridgeGate struct {
	trust      *TrustStore
	autoAllow  map[string]struct{}
	alwaysGate map[string]struct{}
	ttl        time.Duration
}

func NewBridgeGate(trust *TrustStore) *BridgeGate {
	return &BridgeGate{
		trust:     trust,
		autoAllow: parseToolSet(os.Getenv("INFINITY_BRIDGE_AUTOAPPROVE")),
		alwaysGate: parseToolSet(envOr("INFINITY_BRIDGE_BLOCK",
			"fs_save,fs_edit,bash_run,git_commit,git_push,git_pull,git_stage,project_create")),
		ttl: loadApprovalTTL(),
	}
}

func (g *BridgeGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsBridgeTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	lower := strings.ToLower(toolName)
	if _, ok := g.autoAllow[lower]; ok {
		return agent.GateDecision{Allow: true}
	}
	if _, ok := g.alwaysGate[lower]; !ok {
		return agent.GateDecision{Allow: true} // read-only / unknown verb
	}

	// Standing per-session approval shortcut - same pattern as
	// ClaudeCodeGate. If the boss already approved this exact tool for
	// this session within the TTL window, allow without re-prompting.
	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("BridgeGate: approval lookup error: %v", err)
		} else if hasApproval {
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			log.Printf("BridgeGate: %s allowed via prior approval (%s window)", toolName, g.ttl)
			return agent.GateDecision{Allow: true}
		}
	}

	if g.trust == nil {
		log.Printf("BridgeGate: trust store nil, refusing %s", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run " + toolName + " unattended",
		}
	}

	// Render a short human-readable command for the Trust card so the
	// boss can decide without expanding the JSON every time.
	summary := summariseBridgeCall(toolName, input)
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     summary,
		RiskLevel: "high",
		Source:    "bridge_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: "Generic bridge primitive (fs/bash/git) requested a write/exec on the active filesystem. " +
			"Gated because INFINITY_BRIDGE_BLOCK includes this verb.",
		Preview: summary,
	})
	if err != nil {
		log.Printf("BridgeGate: %s queue err=%v", toolName, err)
		return agent.GateDecision{
			Allow:  false,
			Reason: "bridge gate: queue failed: " + err.Error(),
		}
	}
	if id == "" {
		log.Printf("BridgeGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "bridge gate: queue unavailable",
		}
	}
	log.Printf("BridgeGate: %s queued as contract=%s (loop will wait)", toolName, id)
	return agent.GateDecision{
		Allow:           false,
		WaitForApproval: true,
		ContractID:      id,
		Reason:          "queued for Trust approval",
	}
}

// WaitForDecision polls the durable trust contract for resolution.
// Same pattern as ClaudeCodeGate / GitHubGate - single TrustStore is
// the source of truth across all gates.
func (g *BridgeGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
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
				log.Printf("BridgeGate: contract %s approved", contractID)
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

// summariseBridgeCall builds a short one-liner for the Trust card.
// Keeps the JSON detail still available via expansion, but gives the
// boss enough to swipe-approve from a notification.
func summariseBridgeCall(toolName string, input map[string]any) string {
	switch toolName {
	case "fs_save", "fs_edit":
		if p, _ := input["path"].(string); p != "" {
			return toolName + " " + p
		}
	case "bash_run":
		if c, _ := input["cmd"].(string); c != "" {
			if len(c) > 120 {
				c = c[:120] + "…"
			}
			return "bash_run: " + c
		}
	case "git_commit":
		if m, _ := input["message"].(string); m != "" {
			if len(m) > 80 {
				m = m[:80] + "…"
			}
			return "git_commit: " + m
		}
	case "git_push":
		branch, _ := input["branch"].(string)
		remote, _ := input["remote"].(string)
		if remote == "" {
			remote = "origin"
		}
		if branch != "" {
			return "git_push " + remote + " " + branch
		}
		return "git_push " + remote
	case "git_pull":
		return "git_pull"
	case "git_stage":
		return "git_stage"
	case "project_create":
		name, _ := input["name"].(string)
		tmpl, _ := input["template"].(string)
		if tmpl == "" {
			tmpl = "empty"
		}
		github, _ := input["create_github"].(bool)
		if name == "" {
			name = "(unnamed)"
		}
		ghBit := ""
		if github {
			priv, _ := input["private"].(bool)
			visibility := "public"
			if priv {
				visibility = "private"
			}
			ghBit = " + new " + visibility + " GitHub repo"
		}
		return "project_create: " + name + " (" + tmpl + ")" + ghBit
	}
	return toolName
}
