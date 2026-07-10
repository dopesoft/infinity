package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dopesoft/infinity/core/internal/initiative"
	"github.com/dopesoft/infinity/core/internal/mandate"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mandateAnnouncer is the concrete mandate.Announcer — it pings the boss's
// phone when a Mandate closes (the ambient "done", PAI's TTS-on-Stop
// equivalent expressed as a push). Nil-safe via the same Sender guard.
type mandateAnnouncer struct {
	sender *push.Sender
}

func (a *mandateAnnouncer) Announce(ctx context.Context, m *mandate.Mandate) {
	if a == nil || a.sender == nil || m == nil {
		return
	}
	body := m.Summary
	if body == "" {
		body = "All acceptance criteria verified."
	}
	a.sender.Notify(ctx, push.Notification{
		Title: "Done: " + m.Title,
		Body:  body,
		URL:   "/",
		Kind:  "mandate_done",
		Tag:   "mandate-" + m.ID,
	})
}

// initiativeDeliverer is the concrete initiative.Deliverer - it bridges
// the initiative package's urgency policy to the real delivery channels:
// Web Push for urgent notifications, the Phase 1 surface contract for
// normal ones. The initiative package stays dependency-light behind the
// Deliverer interface (same pattern as the workflow engine's adapters).
type initiativeDeliverer struct {
	sender  *push.Sender
	surface *surface.Store
	pool    *pgxpool.Pool
}

type costRecorder struct {
	store *initiative.Store
}

func (r costRecorder) RecordCost(ctx context.Context, category, subject string, costUSD float64, units string, quantity float64, note string) error {
	if r.store == nil {
		return nil
	}
	return r.store.RecordCost(ctx, &initiative.CostEvent{
		Category: category,
		Subject:  subject,
		CostUSD:  costUSD,
		Units:    units,
		Quantity: quantity,
		Note:     note,
	})
}

// Push delivers an urgent notification to the boss's phone via Web Push.
// A nil Sender is safe - the Notifier still logs the notification.
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

// Converse opens a two-way reach-out: a fresh real session (kind='user' -
// the boss replies into it, so it belongs in his sessions list) whose
// opening assistant message is the notification, persisted as a
// 'TaskCompleted' observation - the same durable row a normal assistant
// turn leaves, so the transcript API renders it and the loop hydrates it
// as prior context when the reply arrives. The push deep-links into
// /live?session=<id>; the SW's notificationclick already navigates there.
func (d *initiativeDeliverer) Converse(ctx context.Context, n initiative.Notification) (string, error) {
	if d == nil || d.pool == nil {
		return "", errors.New("no database for conversation delivery")
	}
	source := n.Source
	if source == "" {
		source = "agent"
	}
	originRef, _ := json.Marshal(map[string]string{"kind": "reach_out", "source": source})
	id := uuid.New()
	if _, err := d.pool.Exec(ctx, `
		INSERT INTO mem_sessions (id, kind, origin_ref, started_at)
		VALUES ($1, 'user', $2::jsonb, NOW())
	`, id, string(originRef)); err != nil {
		return "", err
	}

	text := strings.TrimSpace(n.Title)
	if body := strings.TrimSpace(n.Body); body != "" {
		text += "\n\n" + body
	}
	if u := strings.TrimSpace(n.URL); u != "" {
		text += "\n\n" + u
	}
	payload, _ := json.Marshal(map[string]any{"reach_out": true, "source": source})
	if _, err := d.pool.Exec(ctx, `
		INSERT INTO mem_observations (session_id, hook_name, payload, raw_text, importance, created_at)
		VALUES ($1::uuid, 'TaskCompleted', $2::jsonb, $3, 7, NOW())
	`, id, string(payload), text); err != nil {
		return "", err
	}

	if d.sender != nil {
		d.sender.Notify(ctx, push.Notification{
			Title: n.Title,
			Body:  n.Body,
			URL:   "/live?session=" + id.String(),
			Kind:  "reach_out",
			Tag:   "reach-out-" + source,
		})
	}
	return id.String(), nil
}

// Surface puts a normal notification on the dashboard as a generic surface
// item, reusing the Phase 1 surface contract - no bespoke channel.
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
