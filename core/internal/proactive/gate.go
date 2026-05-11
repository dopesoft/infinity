package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/dopesoft/infinity/core/internal/agent"
)

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
}

func NewClaudeCodeGate(trust *TrustStore) *ClaudeCodeGate {
	return &ClaudeCodeGate{
		trust:      trust,
		autoAllow:  parseToolSet(os.Getenv("INFINITY_CLAUDE_CODE_AUTOAPPROVE")),
		alwaysGate: parseToolSet(envOr("INFINITY_CLAUDE_CODE_BLOCK", "bash,write,edit")),
	}
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
	log.Printf("ClaudeCodeGate: %s queued as contract=%s", toolName, id)
	return agent.GateDecision{
		Allow:      false,
		Reason:     "high-risk claude_code call queued for approval",
		ContractID: id,
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
