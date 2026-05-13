package push

import (
	"context"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/proactive"
)

// TrustAdapter implements proactive.TrustNotifier — wire it into the
// TrustStore at boot so every queued contract turns into a banner on
// the boss's iPhone + Mac.
//
//	store := proactive.NewTrustStore(pool)
//	store.SetNotifier(push.NewTrustAdapter(sender))
type TrustAdapter struct {
	Sender *Sender
}

func NewTrustAdapter(sender *Sender) *TrustAdapter {
	return &TrustAdapter{Sender: sender}
}

// NotifyTrustQueued translates a TrustContract into a notification
// payload + delivers it to every active subscription.
//
// The notification deep-links to /trust/<id> so a tap takes the boss
// straight to the approval surface. We use the contract id as the
// `tag` so a repeat insert with the same id wouldn't double-buzz
// (this normally doesn't happen, but Queue is idempotent so the SW
// dedupe is cheap insurance).
func (a *TrustAdapter) NotifyTrustQueued(ctx context.Context, c *proactive.TrustContract) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	if c == nil {
		return
	}
	title := "Approval needed"
	if c.Title != "" {
		title = c.Title
	}
	body := ""
	if c.Preview != "" {
		body = c.Preview
	} else if c.Reasoning != "" {
		body = c.Reasoning
	} else if c.Source != "" {
		body = fmt.Sprintf("Source: %s", c.Source)
	}
	a.Sender.Notify(ctx, Notification{
		Title: title,
		Body:  body,
		Tag:   "trust:" + c.ID,
		URL:   "/trust",
		Kind:  "trust",
		ID:    c.ID,
		// Risk-high contracts require a tap before they dismiss so the
		// boss can't miss them. Everything else dismisses on its own
		// schedule like a normal notification.
		RequireInteraction: c.RiskLevel == "high" || c.RiskLevel == "critical",
	})
}
