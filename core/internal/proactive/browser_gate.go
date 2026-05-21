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

// BrowserGate gates the cloud-browser verbs. The whole point of the browser
// is that the boss can say "go find me leads" and walk away while Jarvis
// drives — so normal browsing (open, navigate, observe, extract, and the
// vast majority of clicks/typing) runs UNATTENDED. Gating every click would
// make it unusable.
//
// The one thing that stops is a TRANSACTIONAL act: a click/submit whose
// label or value says it spends money or destroys an account (buy, pay,
// checkout, place order, delete account, transfer, …). Those queue a Trust
// contract so the boss approves before Jarvis commits something it can't
// undo. The live screencast is the real-time backstop — the boss can watch
// and close the session at any point.
//
// Policy:
//   - browser_open / browser_navigate / browser_observe /
//     browser_extract / browser_close       → allow (read / navigation)
//   - browser_act                            → allow, UNLESS the act looks
//     transactional (content-aware on label+value), then queue Trust
//
// Overrides:
//
//	$INFINITY_BROWSER_AUTOAPPROVE      comma list of browser tools that always allow
//	$INFINITY_BROWSER_GATE_ACTS=true   gate EVERY browser_act, not just transactional
//	$INFINITY_BROWSER_TRANSACTIONAL    extra comma-separated transactional substrings
type BrowserGate struct {
	trust         *TrustStore
	autoAllow     map[string]struct{}
	transactional []string
	gateAllActs   bool
	ttl           time.Duration
}

// defaultTransactional are the substrings that mark a browser_act as
// money-spending or account-destroying. Matched against the act's label and
// value (lowercased). Curated to catch real commits without snagging
// ordinary navigation like "delete filter".
var defaultTransactional = []string{
	"buy", "purchase", "checkout", "check out", "place order", "place bid",
	"complete order", "confirm order", "complete purchase", "pay now", "pay ",
	"payment", "submit payment", "add to cart and", "subscribe", "start subscription",
	"book now", "reserve", "confirm booking", "send money", "transfer", "wire ",
	"delete account", "close account", "deactivate account", "confirm and pay",
	"agree and pay", "place your order", "complete checkout",
}

func NewBrowserGate(trust *TrustStore) *BrowserGate {
	extra := splitCSV(os.Getenv("INFINITY_BROWSER_TRANSACTIONAL"))
	return &BrowserGate{
		trust:         trust,
		autoAllow:     parseToolSet(os.Getenv("INFINITY_BROWSER_AUTOAPPROVE")),
		transactional: append(append([]string{}, defaultTransactional...), extra...),
		gateAllActs:   strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_BROWSER_GATE_ACTS")), "true"),
		ttl:           loadApprovalTTL(),
	}
}

// isTransactionalAct reports whether a browser_act commits money or destroys
// an account, based on the agent-supplied label + value.
func (g *BrowserGate) isTransactionalAct(input map[string]any) bool {
	label, _ := input["label"].(string)
	value, _ := input["value"].(string)
	hay := strings.ToLower(label + " " + value)
	if strings.TrimSpace(hay) == "" {
		return false
	}
	for _, kw := range g.transactional {
		if kw != "" && strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}

func (g *BrowserGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsBrowserTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	lower := strings.ToLower(toolName)
	if _, ok := g.autoAllow[lower]; ok {
		return agent.GateDecision{Allow: true}
	}

	// Only browser_act can be side-effecting; everything else is read/nav.
	if lower != "browser_act" {
		return agent.GateDecision{Allow: true}
	}
	if !g.gateAllActs && !g.isTransactionalAct(input) {
		return agent.GateDecision{Allow: true}
	}

	// Standing per-session approval shortcut — same pattern as the other
	// gates. If the boss already approved browser_act this session within
	// the TTL, allow without re-prompting.
	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("BrowserGate: approval lookup error: %v", err)
		} else if hasApproval {
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			return agent.GateDecision{Allow: true}
		}
	}

	if g.trust == nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run a transactional browser action unattended",
		}
	}

	summary := summariseBrowserAct(input)
	reasoning := "Jarvis is about to take a transactional action in the browser (it spends money or " +
		"destroys an account). Ordinary browsing runs unattended; this one stopped because it can't be undone."
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     summary,
		RiskLevel: "high",
		Source:    "browser_gate",
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
		log.Printf("BrowserGate: queue err=%v", err)
		return agent.GateDecision{Allow: false, Reason: "browser gate: queue failed: " + err.Error()}
	}
	if id == "" {
		return agent.GateDecision{Allow: false, Reason: "browser gate: queue unavailable"}
	}
	log.Printf("BrowserGate: browser_act queued as contract=%s (loop will wait)", id)
	return agent.GateDecision{
		Allow:           false,
		WaitForApproval: true,
		ContractID:      id,
		Reason:          "queued for Trust approval",
	}
}

// WaitForDecision polls the durable trust contract — identical pattern to
// BridgeGate / ClaudeCodeGate (one TrustStore, one approval queue).
func (g *BrowserGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
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

func summariseBrowserAct(input map[string]any) string {
	label, _ := input["label"].(string)
	action, _ := input["action"].(string)
	if label != "" {
		if len(label) > 80 {
			label = label[:80] + "…"
		}
		return "browser: " + action + " \"" + label + "\""
	}
	return "browser: transactional " + action
}

// splitCSV is a tiny comma splitter that trims + lowercases entries.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
