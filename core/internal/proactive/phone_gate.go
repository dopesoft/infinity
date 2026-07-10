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

// PhoneGate gates the `phone_call` tool with CHANNEL-BASED trust:
//
//   - Boss-commissioned calls flow FREE. A request typed/spoken inside the
//     authenticated app (a normal chat/voice session, the dashboard call
//     button) IS the approval — the boss asking is the authorization, and
//     interrupting his own errand with a Trust card breaks the experience
//     (his words). Session kind 'user' = the boss is literally driving.
//   - AUTONOMOUS calls still stop. A cron/sentinel/heartbeat/self-initiated
//     turn deciding to dial a human queues a Trust contract and waits —
//     nobody commissioned that call, so somebody must approve it.
//
// Override:
//
//	$INFINITY_PHONE_AUTOAPPROVE=true   place ALL calls unattended (full trust)
type PhoneGate struct {
	trust       *TrustStore
	autoApprove bool
	// bossSession reports whether a session id is a boss-interactive
	// session (mem_sessions.kind='user') vs an autonomous one. Wired from
	// serve.go; nil = treat everything as autonomous (fail closed).
	bossSession func(ctx context.Context, sessionID string) bool
}

func NewPhoneGate(trust *TrustStore) *PhoneGate {
	return &PhoneGate{
		trust:       trust,
		autoApprove: strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_PHONE_AUTOAPPROVE")), "true"),
	}
}

// SetBossSessionCheck wires the session-kind lookup (serve.go).
func (g *PhoneGate) SetBossSessionCheck(fn func(ctx context.Context, sessionID string) bool) {
	if g != nil {
		g.bossSession = fn
	}
}

func (g *PhoneGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || toolName != "phone_call" {
		return agent.GateDecision{Allow: true}
	}
	if g.autoApprove {
		return agent.GateDecision{Allow: true}
	}
	// The boss asked for this call himself, inside the authenticated app —
	// that IS the approval. (Dashboard call-button sessions are covered by
	// their PreApproveTools grant before this check is ever consulted.)
	if g.bossSession != nil && g.bossSession(ctx, sessionID) {
		return agent.GateDecision{Allow: true}
	}
	if g.trust == nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to place a phone call unattended",
		}
	}

	to, _ := input["to"].(string)
	goal, _ := input["goal"].(string)
	summary := "Jarvis wants to call " + to + ": " + clipForPreview(goal, 140)
	reasoning := "Jarvis is about to place a real phone call in the boss's name. Every call " +
		"stops here for approval — a live human answers, and what's said can't be unsaid."
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     summary,
		RiskLevel: "high",
		Source:    "phone_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: reasoning,
		Preview:   summary,
	})
	if err != nil {
		log.Printf("PhoneGate: queue err=%v", err)
		return agent.GateDecision{Allow: false, Reason: "phone gate: queue failed: " + err.Error()}
	}
	if id == "" {
		return agent.GateDecision{Allow: false, Reason: "phone gate: queue unavailable"}
	}
	infoLog.Printf("PhoneGate: phone_call queued as contract=%s (loop will wait)", id)
	return agent.GateDecision{
		Allow:           false,
		WaitForApproval: true,
		ContractID:      id,
		Reason:          "queued for Trust approval",
	}
}

// WaitForDecision polls the durable trust contract — identical pattern to
// BrowserGate / ClaudeCodeGate (one TrustStore, one approval queue).
func (g *PhoneGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
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

// clipForPreview keeps the approval card title readable.
func clipForPreview(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
