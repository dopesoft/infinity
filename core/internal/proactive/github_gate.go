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

// defaultGitHubBlockList is the suffix block list applied when
// INFINITY_GITHUB_BLOCK is unset. Everything here is a GitHub MCP tool
// whose effect is visible on github.com — opening PRs, merging, pushing
// files, deleting branches, creating repos. Read-only verbs (get_*,
// list_*, search_*, get_me, etc.) intentionally bypass the gate so the
// bot can browse the boss's GitHub without prompting on every call.
//
// Source of names: github/github-mcp-server tool surface as of 2025-11.
// New write verbs may appear over time — extend this list or override
// via INFINITY_GITHUB_BLOCK="<csv>".
const defaultGitHubBlockList = "create_or_update_file,delete_file,push_files," +
	"create_branch," +
	"create_pull_request,update_pull_request,merge_pull_request,update_pull_request_branch," +
	"create_pull_request_review,submit_pending_pull_request_review,delete_pending_pull_request_review," +
	"create_and_submit_pull_request_review,request_copilot_review," +
	"create_issue,update_issue,add_issue_comment,assign_copilot_to_issue," +
	"add_sub_issue,remove_sub_issue,reprioritize_sub_issue," +
	"create_repository,fork_repository," +
	"create_or_update_repository_secret"

// GitHubGate is the agent.ToolGate implementation that routes high-risk
// github__* tool calls through the same Trust queue as ClaudeCodeGate.
//
// Policy (overridable via env):
//
//   - Default-blocked write verbs from github-mcp-server are queued for
//     boss approval (open PR, merge, push files, delete file/branch,
//     create issue/repo, etc.). See defaultGitHubBlockList.
//   - Every other github__* call (get_*, list_*, search_*) passes through.
//   - All approval state lives in mem_trust_contracts (shared with
//     ClaudeCodeGate). One sliding TTL window covers both gates.
//
// Override:
//
//	$INFINITY_GITHUB_AUTOAPPROVE   comma list of tool suffixes that always allow
//	                                (e.g. "create_pull_request" if you live in a CI flow)
//	$INFINITY_GITHUB_BLOCK         comma list of tool suffixes to always queue
//	                                (overrides defaultGitHubBlockList entirely)
type GitHubGate struct {
	trust      *TrustStore
	autoAllow  map[string]struct{}
	alwaysGate map[string]struct{}
	ttl        time.Duration
}

func NewGitHubGate(trust *TrustStore) *GitHubGate {
	return &GitHubGate{
		trust:      trust,
		autoAllow:  parseToolSet(os.Getenv("INFINITY_GITHUB_AUTOAPPROVE")),
		alwaysGate: parseToolSet(envOr("INFINITY_GITHUB_BLOCK", defaultGitHubBlockList)),
		ttl:        loadApprovalTTL(),
	}
}

func (g *GitHubGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsGitHubTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	suffix := strings.ToLower(strings.TrimPrefix(toolName, "github__"))

	if _, ok := g.autoAllow[suffix]; ok {
		return agent.GateDecision{Allow: true}
	}
	if _, ok := g.alwaysGate[suffix]; !ok {
		// Not in the block list: default allow. Read-only verbs land here.
		return agent.GateDecision{Allow: true}
	}

	// Same durable approval check as ClaudeCodeGate: a single boss approval
	// for (session, tool) covers the whole TTL window so the bot doesn't
	// re-prompt for every comment in a long-running PR thread.
	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("GitHubGate: approval lookup error: %v", err)
		} else if hasApproval {
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			log.Printf("GitHubGate: %s allowed via prior approval (durable, %s window)",
				toolName, g.ttl)
			return agent.GateDecision{Allow: true}
		}
	}

	if g.trust == nil {
		log.Printf("GitHubGate: trust store nil, refusing %s", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run github__" + suffix + " unattended",
		}
	}

	preview := buildPreview(toolName, input)
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     fmt.Sprintf("Run %s on github.com", toolName),
		RiskLevel: "high",
		Source:    "github_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: "GitHub MCP requested a write/merge/create on github.com. Gated for safety because " +
			"INFINITY_GITHUB_BLOCK includes this verb.",
		Preview: preview,
	})
	if err != nil {
		log.Printf("GitHubGate: %s queue err=%v", toolName, err)
		return agent.GateDecision{
			Allow:  false,
			Reason: "could not queue trust contract: " + err.Error(),
		}
	}
	if id == "" {
		log.Printf("GitHubGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store unavailable; row was NOT persisted — do not tell the boss it was queued",
		}
	}
	log.Printf("GitHubGate: %s queued as contract=%s (loop will wait)", toolName, id)
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     15 * time.Minute,
		Preview:         preview,
	}
}

// WaitForDecision mirrors ClaudeCodeGate.WaitForDecision. Both gates poll
// the same mem_trust_contracts table; the only difference is the log line
// prefix so audit traces stay attributable.
func (g *GitHubGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
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
				log.Printf("GitHubGate: contract %s approved", contractID)
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
