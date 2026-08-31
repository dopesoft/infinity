package proactive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/purchase"
)

// PurchaseGate stops every purchase and shows the boss exactly what he is
// agreeing to.
//
// # WHAT MAKES THIS DIFFERENT FROM THE OTHER GATES
//
// Every other gate approves a TOOL CALL. This one approves an OBLIGATION: the
// merchant, the cart, the exact total, the card, the deadline. That difference
// is the whole feature. An approval attached to a click can be spent on a
// different click; an approval attached to an obligation cannot outlive the
// terms it was given for, because the fill boundary re-checks those terms
// against the live page before it pays.
//
// THREE THINGS THIS GATE DELIBERATELY DOES NOT DO
//
//  1. NO standing-approval shortcut. Every other gate calls
//     HasRecentApprovalForTool so a second call in the same session runs
//     unattended. Copying that here would let the second purchase in a
//     conversation go through with no card shown at all. Every purchase stops,
//     every time. That is the boss's stated policy and it is also just correct.
//
//  2. NO deferred execution. It marks the contract non-deferrable, so
//     TrustExecutor will not replay it hours later against a browser session
//     that has since been reaped and a cart that has since changed.
//
//  3. NO retry on denial. A denied purchase ends as bossDenied in the loop,
//     which is the one failure class self-heal is forbidden to work around.
type PurchaseGate struct {
	trust *TrustStore
	store *purchase.Store
	ttl   time.Duration
}

func NewPurchaseGate(trust *TrustStore, store *purchase.Store) *PurchaseGate {
	return &PurchaseGate{trust: trust, store: store, ttl: 15 * time.Minute}
}

// IsPurchaseTool reports whether a tool spends money.
func IsPurchaseTool(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "purchase_execute")
}

func (g *PurchaseGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !IsPurchaseTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	if g.trust == nil || g.store == nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: "I cannot reach the approval queue, and I will not spend money without it.",
		}
	}
	id, _ := input["obligation_id"].(string)
	o, err := g.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: "I could not find that purchase, so there is nothing for the boss to approve. Bind it with purchase_propose first.",
		}
	}

	switch o.Status {
	case purchase.StatusApproved:
		// He already said yes to THIS obligation and it has not been claimed.
		// The claim inside the tool is what keeps that single-shot.
		return agent.GateDecision{Allow: true}
	case purchase.StatusDraft, purchase.StatusPending:
		// Fall through and ask.
	default:
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("That purchase is %s, so there is nothing to run. Do not try again; "+
				"use purchase_status to see how it ended.", o.Status),
		}
	}

	if time.Now().After(o.ExpiresAt) {
		return agent.GateDecision{
			Allow:  false,
			Reason: "That purchase has expired, so the total on it is no longer trustworthy. Re-read the checkout and propose it again.",
		}
	}

	preview := Preview(o)
	contractID, err := g.trust.Queue(ctx, &TrustContract{
		Title:     fmt.Sprintf("Buy %s at %s", o.Total(), merchantLabel(o)),
		RiskLevel: "critical",
		Source:    "purchase_gate",
		ActionSpec: map[string]any{
			"tool":          toolName,
			"input":         input,
			"session_id":    sessionID,
			"project":       project,
			"obligation_id": o.ID,
			// Never replay a purchase outside its own session. See
			// TrustExecutor.claim, which honours this generically.
			"deferrable": false,
		},
		Reasoning: "This spends real money. Approving it authorises this exact cart at this exact total and nothing else: " +
			"if the checkout changes before it runs, I stop instead of paying a different amount.",
		Preview: preview,
	})
	if err != nil || contractID == "" {
		return agent.GateDecision{
			Allow:  false,
			Reason: "I could not queue that purchase for approval, so I have not spent anything.",
		}
	}
	if o.Status == purchase.StatusDraft {
		_ = g.store.AwaitApproval(ctx, o.ID, contractID)
	}

	return agent.GateDecision{
		Allow:           false,
		WaitForApproval: true,
		ContractID:      contractID,
		WaitTimeout:     g.ttl,
		Preview:         preview,
		Reason:          "waiting for the boss to approve this purchase",
	}
}

// Preview is what the boss actually reads before saying yes.
//
// Merchant, exact total with its currency, every line item, and the last four
// of the card. Two carts from one merchant otherwise render as the same
// sentence, and an approval card you cannot tell apart from another one is not
// really an approval.
func Preview(o *purchase.Obligation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Buy from %s\n", merchantLabel(o))
	fmt.Fprintf(&b, "Total: %s\n", o.Total())
	if len(o.Cart) > 0 {
		b.WriteString("\n")
		for _, li := range o.Cart {
			fmt.Fprintf(&b, "  %s", li.Title)
			if li.Quantity > 1 {
				fmt.Fprintf(&b, " x%d", li.Quantity)
			}
			if li.UnitCents > 0 {
				fmt.Fprintf(&b, "  %.2f", float64(li.UnitCents)/100)
			}
			b.WriteString("\n")
		}
	}
	if o.CardID != "" {
		b.WriteString("\nPaying with the card on file.")
	} else {
		b.WriteString("\nPaying with the card the merchant already has.")
	}
	fmt.Fprintf(&b, "\nIf the checkout changes before this runs, I stop instead of paying.\nRef %s", short(o.IdempotencyKey))
	return b.String()
}

func merchantLabel(o *purchase.Obligation) string {
	if strings.TrimSpace(o.MerchantName) != "" {
		return fmt.Sprintf("%s (%s)", o.MerchantName, o.MerchantHost)
	}
	return o.MerchantHost
}

func short(fingerprint string) string {
	if len(fingerprint) > 8 {
		return fingerprint[:8]
	}
	return fingerprint
}

// WaitForDecision blocks the loop until the boss decides.
//
// On approval it moves the obligation to approved, so the re-executed tool call
// finds a claimable purchase. It consumes the contract BY ID rather than by
// (tool, session): with two purchases open in one conversation, matching by
// tool would consume the wrong one and leave the other approved for
// TrustExecutor to find.
func (g *PurchaseGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if g == nil || g.trust == nil || contractID == "" {
		return false, "no approval queue"
	}
	if timeout <= 0 {
		timeout = g.ttl
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return false, fmt.Sprintf("timed out waiting for approval (%s), so I did not buy anything", timeout)
		case <-tick.C:
			status, _, _, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue
			}
			switch status {
			case "approved":
				_, _ = g.trust.ConsumeByID(waitCtx, contractID)
				g.approveObligation(waitCtx, contractID)
				return true, ""
			case "consumed":
				g.approveObligation(waitCtx, contractID)
				return true, ""
			case "denied":
				return false, "the boss said no, so nothing was bought"
			case "snoozed":
				return false, "the boss snoozed this, so nothing was bought"
			}
		}
	}
}

func (g *PurchaseGate) approveObligation(ctx context.Context, contractID string) {
	if g.store == nil {
		return
	}
	id := g.obligationFor(ctx, contractID)
	if id == "" {
		return
	}
	_ = g.store.Approve(ctx, id)
}

// obligationFor reads the obligation id back off the contract, so the link
// survives a process restart between queueing and approval.
func (g *PurchaseGate) obligationFor(ctx context.Context, contractID string) string {
	if g.trust == nil || g.trust.pool == nil {
		return ""
	}
	var id string
	err := g.trust.pool.QueryRow(ctx,
		`SELECT COALESCE(action_spec->>'obligation_id','') FROM mem_trust_contracts WHERE id = $1::uuid`,
		contractID).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}
