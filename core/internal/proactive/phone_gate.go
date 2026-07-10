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

// PhoneGate gates the `phone_call` tool. A phone call is the highest-touch
// thing Jarvis can do — a real human answers, and whatever the call agent
// says is said in the boss's name. Unlike browsing (where only transactional
// acts stop), EVERY outbound call queues a Trust contract and waits for the
// boss's explicit approval before Twilio dials. There is deliberately no
// per-session standing-approval shortcut: approving one call must never
// silently authorize the next one to a different number.
//
// Override:
//
//	$INFINITY_PHONE_AUTOAPPROVE=true   place calls unattended (full trust)
type PhoneGate struct {
	trust       *TrustStore
	autoApprove bool
}

func NewPhoneGate(trust *TrustStore) *PhoneGate {
	return &PhoneGate{
		trust:       trust,
		autoApprove: strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_PHONE_AUTOAPPROVE")), "true"),
	}
}

func (g *PhoneGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || toolName != "phone_call" {
		return agent.GateDecision{Allow: true}
	}
	if g.autoApprove {
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
