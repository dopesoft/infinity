// Package reauth implements park-and-resume for model credential failures.
//
// When the agent loop's active brain hits an auth failure (revoked ChatGPT
// OAuth token, invalid Anthropic key, …), the turn is PARKED here instead of
// hard-erroring. A background poller probes whether the active brain works
// again — the moment it does (the boss re-authed, OR switched to a healthy
// model), the poller REPLAYS the parked turn and delivers the answer into the
// originating chat session. Deterministic Go (a ticker), not LLM cognition;
// durable across restarts via mem_reauth_waits (migration 092). Mirrors the
// watch_until substrate (mem_watches + watch.Poller).
package reauth

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog routes success/lifecycle lines to stdout so Railway tags them
// severity:info (stderr → severity:error). Failures keep using stdlib log.
var infoLog = log.New(os.Stdout, "reauth: ", log.LstdFlags)

// Notifier delivers a message into a live chat session (and optionally a push).
// Satisfied by the same adapter the watch poller uses.
type Notifier interface {
	Notify(sessionID, text string)
}

// Replayer re-runs a parked turn for a session and returns the assistant's
// final text. Implemented over agent.Loop.Run in the serve command.
type Replayer interface {
	Replay(ctx context.Context, sessionID, userText, model string) (string, error)
}

// HealthProber reports whether the agent's CURRENT active brain can serve a
// request. It probes the live provider, so it returns true whether the boss
// re-authed the failed model or switched the active model to a healthy one.
type HealthProber interface {
	Healthy(ctx context.Context) bool
}

// Store is the persistence boundary for mem_reauth_waits. Park is called from
// the agent loop; the poller reads/settles. Pure persistence — chat
// notification lives in the loop's parker adapter so this stays dependency-free.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		return nil
	}
	return &Store{pool: pool}
}

// Park records (or refreshes) the waiting turn for a session. One waiting row
// per session — a repeat failure updates reason/text rather than stacking.
func (s *Store) Park(ctx context.Context, sessionID, provider, model, userText, reason string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_reauth_waits
		  (session_id, provider, model, user_text, reason, status, next_probe_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'waiting', NOW(), NOW() + INTERVAL '24 hours')
		ON CONFLICT (session_id) WHERE status = 'waiting'
		DO UPDATE SET
		  provider      = EXCLUDED.provider,
		  model         = EXCLUDED.model,
		  user_text     = CASE WHEN EXCLUDED.user_text <> '' THEN EXCLUDED.user_text
		                       ELSE mem_reauth_waits.user_text END,
		  reason        = EXCLUDED.reason,
		  next_probe_at = NOW()
	`, sessionID, provider, model, userText, reason)
	return err
}

type wait struct {
	id        string
	sessionID string
	provider  string
	model     string
	userText  string
	expiresAt time.Time
}

// Poller probes brain health and replays parked turns. Nil-safe.
type Poller struct {
	store    *Store
	prober   HealthProber
	replayer Replayer
	notifier Notifier
	interval time.Duration
	backoff  time.Duration
}

// NewPoller returns a Poller, or nil when any dependency is missing.
func NewPoller(store *Store, prober HealthProber, replayer Replayer, n Notifier) *Poller {
	if store == nil || prober == nil || replayer == nil || n == nil {
		return nil
	}
	return &Poller{
		store:    store,
		prober:   prober,
		replayer: replayer,
		notifier: n,
		interval: 15 * time.Second, // only matters while something is parked
		backoff:  20 * time.Second, // re-probe cadence per still-broken wait
	}
}

// Start runs the tick loop until ctx is cancelled. Waits are durable in
// mem_reauth_waits, so a restart resumes them on the next tick.
func (p *Poller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	infoLog.Printf("reauth poller started (interval=%s)", p.interval)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	due, err := p.listDue(ctx)
	if err != nil {
		log.Printf("reauth: list due: %v", err)
		return
	}
	if len(due) == 0 {
		return // nothing parked → no probe, no cost
	}

	// Expire anything past its deadline first (and tell the boss it lapsed).
	now := time.Now()
	live := due[:0]
	for _, d := range due {
		if now.After(d.expiresAt) {
			p.expire(ctx, d)
			continue
		}
		live = append(live, d)
	}
	if len(live) == 0 {
		return
	}

	// One probe per tick covers every parked wait: they all resume on whatever
	// brain is currently healthy.
	if !p.prober.Healthy(ctx) {
		p.bump(ctx, live)
		return
	}

	for _, d := range live {
		p.resume(ctx, d)
	}
}

func (p *Poller) resume(ctx context.Context, d wait) {
	// Replay on the CURRENT active model (empty model → loop resolves it), so a
	// turn parked under a dead ChatGPT token resumes fine even if the boss
	// switched the active model to Anthropic.
	answer, err := p.replayer.Replay(ctx, d.sessionID, d.userText, "")
	if err != nil {
		// Replay itself failed — likely the credential flapped back. Leave the
		// row waiting and re-probe on the next cycle.
		log.Printf("reauth: replay session=%s: %v", d.sessionID, err)
		p.bump(ctx, []wait{d})
		return
	}
	p.markResumed(ctx, d.id)
	infoLog.Printf("resumed parked turn session=%s provider=%s", d.sessionID, d.provider)
	// The replayed turn's answer is delivered to the boss as a proactive
	// message (loop.Run streamed into our buffer, not the live WS).
	msg := "✅ Reconnected — here's what you asked for:\n\n" + answer
	p.notifier.Notify(d.sessionID, msg)
}

func (p *Poller) expire(ctx context.Context, d wait) {
	p.markExpired(ctx, d.id)
	p.notifier.Notify(d.sessionID,
		"⚠️ I couldn't auto-resume your last request — the model credential is still not reconnected after 24h. Reconnect it (or switch models) and re-send.")
}

func (p *Poller) listDue(ctx context.Context) ([]wait, error) {
	rows, err := p.store.pool.Query(ctx, `
		SELECT id::text, session_id::text, provider, model, user_text, expires_at
		  FROM mem_reauth_waits
		 WHERE status = 'waiting' AND next_probe_at <= NOW()
		 ORDER BY next_probe_at
		 LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wait
	for rows.Next() {
		var w wait
		if err := rows.Scan(&w.id, &w.sessionID, &w.provider, &w.model, &w.userText, &w.expiresAt); err != nil {
			log.Printf("reauth: scan: %v", err)
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

func (p *Poller) bump(ctx context.Context, ws []wait) {
	for _, w := range ws {
		if _, err := p.store.pool.Exec(ctx,
			`UPDATE mem_reauth_waits SET next_probe_at = NOW() + $2 WHERE id = $1 AND status='waiting'`,
			w.id, p.backoff); err != nil {
			log.Printf("reauth: bump %s: %v", w.id, err)
		}
	}
}

func (p *Poller) markResumed(ctx context.Context, id string) {
	if _, err := p.store.pool.Exec(ctx,
		`UPDATE mem_reauth_waits SET status='resumed', resumed_at=NOW() WHERE id=$1`, id); err != nil {
		log.Printf("reauth: mark resumed %s: %v", id, err)
	}
}

func (p *Poller) markExpired(ctx context.Context, id string) {
	if _, err := p.store.pool.Exec(ctx,
		`UPDATE mem_reauth_waits SET status='expired' WHERE id=$1`, id); err != nil {
		log.Printf("reauth: mark expired %s: %v", id, err)
	}
}
