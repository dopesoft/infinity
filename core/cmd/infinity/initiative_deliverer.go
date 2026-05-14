package main

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/initiative"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// initiativeDeliverer is the concrete initiative.Deliverer — it bridges
// the initiative package's urgency policy to the real delivery channels:
// Web Push for urgent notifications, the Phase 1 surface contract for
// normal ones. The initiative package stays dependency-light behind the
// Deliverer interface (same pattern as the workflow engine's adapters).
type initiativeDeliverer struct {
	sender  *push.Sender
	surface *surface.Store
}

// Push delivers an urgent notification to the boss's phone via Web Push.
// A nil Sender is safe — the Notifier still logs the notification.
func (d *initiativeDeliverer) Push(ctx context.Context, n initiative.Notification) error {
	if d == nil || d.sender == nil {
		return nil
	}
	d.sender.Notify(ctx, push.Notification{
		Title: n.Title,
		Body:  n.Body,
		URL:   n.URL,
		Kind:  "agent_initiative",
		Tag:   "initiative-" + n.Source,
	})
	return nil
}

// Surface puts a normal notification on the dashboard as a generic surface
// item, reusing the Phase 1 surface contract — no bespoke channel.
func (d *initiativeDeliverer) Surface(ctx context.Context, n initiative.Notification) error {
	if d == nil || d.surface == nil {
		return nil
	}
	importance := 60
	if n.Urgency == initiative.UrgencyUrgent {
		importance = 90
	}
	_, err := d.surface.Upsert(ctx, &surface.Item{
		Surface:    "alerts",
		Kind:       "alert",
		Source:     "initiative:" + n.Source,
		Title:      n.Title,
		Body:       n.Body,
		URL:        n.URL,
		Importance: &importance,
	})
	return err
}
