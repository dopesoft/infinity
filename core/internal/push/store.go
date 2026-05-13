// Package push wires Web Push delivery from Core to the boss's iPhone
// and macOS PWAs.
//
// The store layer wraps mem_push_subscriptions — one row per installed
// browser/device that has opted in. The sender layer encrypts and
// delivers payloads via the boss's push services (FCM for Chrome/Edge,
// APNs for Safari/iOS) using VAPID-signed JWTs.
//
// The HTTP layer (api.go) exposes:
//   - GET  /api/push/vapid       — public key for the client to subscribe
//   - POST /api/push/subscribe   — register a new device
//   - POST /api/push/unsubscribe — remove a device
//   - GET  /api/push/devices     — list registered devices
//   - POST /api/push/test        — send a test notification to all devices
//
// Other packages (proactive, voyager, sentinel, …) inject the *Sender
// and call Notify(ctx, payload) on the events they care about — trust
// inserts, curiosity questions, sentinel fires, etc.
package push

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Subscription mirrors mem_push_subscriptions. The crypto material is
// returned alongside the endpoint because the webpush sender needs all
// three to encrypt + deliver a payload.
type Subscription struct {
	ID         uuid.UUID  `json:"id"`
	Endpoint   string     `json:"endpoint"`
	P256dh     string     `json:"p256dh"`
	AuthKey    string     `json:"-"` // omit from API responses; sensitive
	Label      string     `json:"label"`
	UserAgent  string     `json:"user_agent"`
	Revoked    bool       `json:"revoked"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Store handles persistence for push subscriptions.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Upsert inserts or refreshes a subscription. Endpoint is the unique
// key — Web Push spec guarantees a single endpoint per (device, app).
// If the endpoint already exists we update the crypto keys (they can
// rotate when a browser refreshes subscriptions) and clear `revoked`.
func (s *Store) Upsert(ctx context.Context, sub Subscription) (Subscription, error) {
	if s == nil || s.pool == nil {
		return Subscription{}, errors.New("push store not configured")
	}
	if sub.Endpoint == "" || sub.P256dh == "" || sub.AuthKey == "" {
		return Subscription{}, errors.New("endpoint + p256dh + auth are required")
	}
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	const q = `
		INSERT INTO mem_push_subscriptions
			(id, endpoint, p256dh, auth_key, label, user_agent, revoked, last_seen_at, created_at)
		VALUES
			($1, $2, $3, $4, $5, $6, false, NOW(), NOW())
		ON CONFLICT (endpoint) DO UPDATE
		SET p256dh       = EXCLUDED.p256dh,
		    auth_key     = EXCLUDED.auth_key,
		    label        = COALESCE(NULLIF(EXCLUDED.label, ''), mem_push_subscriptions.label),
		    user_agent   = COALESCE(NULLIF(EXCLUDED.user_agent, ''), mem_push_subscriptions.user_agent),
		    revoked      = false,
		    last_seen_at = NOW()
		RETURNING id, endpoint, p256dh, auth_key, label, user_agent, revoked, last_seen_at, created_at
	`
	row := s.pool.QueryRow(ctx, q,
		sub.ID, sub.Endpoint, sub.P256dh, sub.AuthKey, sub.Label, sub.UserAgent,
	)
	var out Subscription
	if err := row.Scan(
		&out.ID, &out.Endpoint, &out.P256dh, &out.AuthKey,
		&out.Label, &out.UserAgent, &out.Revoked, &out.LastSeenAt, &out.CreatedAt,
	); err != nil {
		return Subscription{}, err
	}
	return out, nil
}

// Delete removes a subscription by endpoint. Used by the unsubscribe
// endpoint and by the sender when the push service returns 410 Gone.
func (s *Store) Delete(ctx context.Context, endpoint string) error {
	if s == nil || s.pool == nil {
		return errors.New("push store not configured")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM mem_push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

// MarkRevoked flips the revoked flag without deleting the row. We use
// this when a push service returns a non-410 error so the boss can see
// the failing device in Settings and decide whether to retry or remove.
func (s *Store) MarkRevoked(ctx context.Context, endpoint string) error {
	if s == nil || s.pool == nil {
		return errors.New("push store not configured")
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE mem_push_subscriptions SET revoked = true WHERE endpoint = $1`,
		endpoint)
	return err
}

// Active returns every non-revoked subscription. The sender fans out to
// all of these for "notify the boss" style events.
func (s *Store) Active(ctx context.Context) ([]Subscription, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint, p256dh, auth_key, label, user_agent, revoked, last_seen_at, created_at
		FROM mem_push_subscriptions
		WHERE NOT revoked
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubs(rows)
}

// All returns every subscription, revoked or not. Used by the devices
// listing endpoint in Settings → Notifications.
func (s *Store) All(ctx context.Context) ([]Subscription, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint, p256dh, auth_key, label, user_agent, revoked, last_seen_at, created_at
		FROM mem_push_subscriptions
		ORDER BY revoked ASC, created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubs(rows)
}

// Touch updates last_seen_at on a successful delivery. Used by the
// sender to give Settings a "last delivered at" hint.
func (s *Store) Touch(ctx context.Context, endpoint string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE mem_push_subscriptions SET last_seen_at = NOW() WHERE endpoint = $1`,
		endpoint)
	return err
}

func scanSubs(rows pgx.Rows) ([]Subscription, error) {
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(
			&s.ID, &s.Endpoint, &s.P256dh, &s.AuthKey,
			&s.Label, &s.UserAgent, &s.Revoked, &s.LastSeenAt, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
