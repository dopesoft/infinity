// Package initiative implements the initiative + economics substrate -
// Phase 6 (final) of the assembly substrate.
//
// An always-on agent needs two things a reactive chatbot doesn't:
//
//   - INITIATIVE: a policy for when and how to reach the boss. The agent
//     decides an urgency; the Notifier routes it - urgent → push to the
//     phone now, normal → a dashboard card, low → batched into the next
//     digest. Everything is logged to mem_notifications.
//   - ECONOMICS: awareness of what it costs to run. Every cost-incurring
//     event lands in mem_cost_events; a budget rollup lets the agent make
//     value/cost tradeoffs instead of burning the budget blind.
//
// The agent reaches out via the notify tool, records spend via cost_record,
// and reads the budget via budget_status - never raw SQL.
package initiative

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Urgency drives the routing policy.
type Urgency string

const (
	UrgencyUrgent Urgency = "urgent" // push to the phone now
	UrgencyNormal Urgency = "normal" // a dashboard card
	UrgencyLow    Urgency = "low"    // batched into the next digest
)

func (u Urgency) Valid() bool {
	return u == UrgencyUrgent || u == UrgencyNormal || u == UrgencyLow
}

// Notification is one outbound message from the agent to the boss.
type Notification struct {
	ID          string     `json:"id"`
	Urgency     Urgency    `json:"urgency"`
	Title       string     `json:"title"`
	Body        string     `json:"body,omitempty"`
	URL         string     `json:"url,omitempty"`
	Source      string     `json:"source"`
	Channel     string     `json:"channel"` // push | surface | digest
	Status      string     `json:"status"`  // sent | batched
	CreatedAt   time.Time  `json:"createdAt"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
}

// CostEvent is one recorded cost-incurring event.
type CostEvent struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"` // llm | api | tool | workflow | …
	Subject   string    `json:"subject,omitempty"`
	CostUSD   float64   `json:"costUsd"`
	Units     string    `json:"units,omitempty"`
	Quantity  float64   `json:"quantity,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Budget is the rollup of cost over a window.
type Budget struct {
	WindowDays   int                `json:"windowDays"`
	TotalUSD     float64            `json:"totalUsd"`
	ByCategory   map[string]float64 `json:"byCategory"`
	EventCount   int                `json:"eventCount"`
	LimitUSD     float64            `json:"limitUsd"` // INFINITY_BUDGET_USD, 0 = no limit
	WithinBudget bool               `json:"withinBudget"`
	Note         string             `json:"note"`
}

// Deliverer is how the initiative package reaches the boss without
// importing the push / surface packages. serve.go provides the impl
// (push.Sender for Push, surface.Store for Surface).
type Deliverer interface {
	Push(ctx context.Context, n Notification) error
	Surface(ctx context.Context, n Notification) error
}

// Store is the persistence boundary for mem_notifications + mem_cost_events.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{pool: pool, logger: logger}
}

// recordNotification persists one notification row.
func (s *Store) recordNotification(ctx context.Context, n *Notification) error {
	var delivered *time.Time
	if n.Status == "sent" {
		now := time.Now().UTC()
		delivered = &now
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_notifications
		  (urgency, title, body, url, source, channel, status, delivered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id::text
	`, string(n.Urgency), n.Title, n.Body, n.URL, n.Source, n.Channel, n.Status, delivered).Scan(&id)
	if err != nil {
		return fmt.Errorf("initiative: record notification: %w", err)
	}
	n.ID = id
	return nil
}

// listBatched returns notifications waiting for the next digest flush.
func (s *Store) listBatched(ctx context.Context, limit int) ([]*Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, urgency, title, body, url, source, channel, status, created_at
		  FROM mem_notifications WHERE status = 'batched'
		 ORDER BY created_at ASC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		var (
			n       Notification
			urgency string
		)
		if err := rows.Scan(&n.ID, &urgency, &n.Title, &n.Body, &n.URL, &n.Source, &n.Channel, &n.Status, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Urgency = Urgency(urgency)
		out = append(out, &n)
	}
	return out, rows.Err()
}

// markDelivered flips a set of batched notifications to sent.
func (s *Store) markDelivered(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_notifications
		   SET status = 'sent', delivered_at = NOW()
		 WHERE id = ANY($1::uuid[])
	`, ids)
	return err
}

// RecordCost persists one cost event.
func (s *Store) RecordCost(ctx context.Context, e *CostEvent) error {
	if s == nil || s.pool == nil {
		return errors.New("initiative: no pool")
	}
	if e.Category == "" {
		return errors.New("initiative: cost category is required")
	}
	if e.CostUSD < 0 {
		e.CostUSD = 0
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_cost_events (category, subject, cost_usd, units, quantity, note)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id::text
	`, e.Category, e.Subject, e.CostUSD, e.Units, e.Quantity, e.Note).Scan(&id)
	if err != nil {
		return fmt.Errorf("initiative: record cost: %w", err)
	}
	e.ID = id
	return nil
}

// BudgetRollup sums cost over the last `windowDays` days, broken down by
// category, and compares it against INFINITY_BUDGET_USD (per-window limit;
// unset/0 means no limit).
func (s *Store) BudgetRollup(ctx context.Context, windowDays int) (*Budget, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("initiative: no pool")
	}
	if windowDays <= 0 || windowDays > 365 {
		windowDays = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT category, COALESCE(SUM(cost_usd), 0)::float8, COUNT(*)::int
		  FROM mem_cost_events
		 WHERE created_at >= NOW() - make_interval(days => $1)
		 GROUP BY category
	`, windowDays)
	if err != nil {
		return nil, fmt.Errorf("initiative: budget rollup: %w", err)
	}
	defer rows.Close()

	b := &Budget{WindowDays: windowDays, ByCategory: map[string]float64{}}
	for rows.Next() {
		var (
			category string
			sum      float64
			count    int
		)
		if err := rows.Scan(&category, &sum, &count); err != nil {
			return nil, err
		}
		b.ByCategory[category] = round2(sum)
		b.TotalUSD += sum
		b.EventCount += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	b.TotalUSD = round2(b.TotalUSD)

	if v := strings.TrimSpace(os.Getenv("INFINITY_BUDGET_USD")); v != "" {
		if limit, perr := strconv.ParseFloat(v, 64); perr == nil && limit > 0 {
			b.LimitUSD = limit
		}
	}
	b.WithinBudget = b.LimitUSD == 0 || b.TotalUSD <= b.LimitUSD
	switch {
	case b.LimitUSD == 0:
		b.Note = "No budget limit set (INFINITY_BUDGET_USD)."
	case !b.WithinBudget:
		b.Note = fmt.Sprintf("OVER BUDGET - $%.2f of $%.2f over the last %d days. Throttle expensive work.", b.TotalUSD, b.LimitUSD, windowDays)
	default:
		b.Note = fmt.Sprintf("$%.2f of $%.2f used over the last %d days.", b.TotalUSD, b.LimitUSD, windowDays)
	}
	return b, nil
}

// Notifier applies the urgency routing policy on top of the Store + a
// Deliverer.
type Notifier struct {
	store     *Store
	deliverer Deliverer
	logger    *slog.Logger
}

func NewNotifier(store *Store, deliverer Deliverer, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{store: store, deliverer: deliverer, logger: logger}
}

// Send routes a notification by urgency:
//
//	urgent → Deliverer.Push  (the phone, now)        channel=push   status=sent
//	normal → Deliverer.Surface (a dashboard card)    channel=surface status=sent
//	low    → held for the next digest flush          channel=digest  status=batched
//
// Always logged to mem_notifications regardless of channel.
func (n *Notifier) Send(ctx context.Context, notif *Notification) error {
	if n == nil || n.store == nil {
		return errors.New("initiative: no store")
	}
	if strings.TrimSpace(notif.Title) == "" {
		return errors.New("initiative: notification title is required")
	}
	if !notif.Urgency.Valid() {
		notif.Urgency = UrgencyNormal
	}
	if notif.Source == "" {
		notif.Source = "agent"
	}

	switch notif.Urgency {
	case UrgencyUrgent:
		notif.Channel, notif.Status = "push", "sent"
		if n.deliverer != nil {
			if err := n.deliverer.Push(ctx, *notif); err != nil {
				// Delivery failed - still log it, but as surface so it's
				// not lost.
				n.logger.Error("initiative: push failed, falling back to surface", "err", err)
				notif.Channel = "surface"
				if n.deliverer != nil {
					_ = n.deliverer.Surface(ctx, *notif)
				}
			}
		}
	case UrgencyLow:
		notif.Channel, notif.Status = "digest", "batched"
	default: // normal
		notif.Channel, notif.Status = "surface", "sent"
		if n.deliverer != nil {
			if err := n.deliverer.Surface(ctx, *notif); err != nil {
				n.logger.Error("initiative: surface failed", "err", err)
			}
		}
	}
	return n.store.recordNotification(ctx, notif)
}

// Digest flushes every batched (low-urgency) notification into a single
// push, then marks them delivered. Returns the count flushed.
func (n *Notifier) Digest(ctx context.Context) (int, error) {
	if n == nil || n.store == nil {
		return 0, errors.New("initiative: no store")
	}
	batched, err := n.store.listBatched(ctx, 50)
	if err != nil {
		return 0, err
	}
	if len(batched) == 0 {
		return 0, nil
	}
	var b strings.Builder
	ids := make([]string, 0, len(batched))
	for i, item := range batched {
		fmt.Fprintf(&b, "%d. %s", i+1, item.Title)
		if item.Body != "" {
			fmt.Fprintf(&b, " - %s", item.Body)
		}
		b.WriteByte('\n')
		ids = append(ids, item.ID)
	}
	digest := Notification{
		Urgency: UrgencyNormal,
		Title:   fmt.Sprintf("Digest - %d update(s)", len(batched)),
		Body:    strings.TrimSpace(b.String()),
		Source:  "digest",
		Channel: "push",
		Status:  "sent",
	}
	if n.deliverer != nil {
		if err := n.deliverer.Push(ctx, digest); err != nil {
			n.logger.Error("initiative: digest push failed", "err", err)
		}
	}
	_ = n.store.recordNotification(ctx, &digest)
	if err := n.store.markDelivered(ctx, ids); err != nil {
		return 0, err
	}
	return len(batched), nil
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
