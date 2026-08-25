package surface

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// body_backfill.go — the self-heal half of the body-capture mechanic.
//
// Capture-at-Upsert fixes every message surfaced from now on. It does nothing
// for the ones already on the boss's dashboard with nothing behind them: on
// 2026-08-25 that was 356 of 390 open follow-ups, some going back to June, each
// one a card he can tap that opens onto an empty pane. This sweep fills them.
//
// It is not a one-shot script, because the same hole reopens every time a fetch
// fails transiently (a rate limit, a reconnect in progress, a 5xx). Running it
// on a ticker means a message that couldn't be captured at surface time gets
// picked up minutes later, and the dashboard converges on "every card has its
// message" without anyone remembering to go and fix it.
//
// Bounded on purpose: a batch per tick, a hard attempt ceiling per row. A
// mailbox that is genuinely gone (revoked with no reconnected twin) stops being
// retried after maxBodyAttempts and keeps its recorded reason, so the failure
// stays legible instead of turning into an infinite quiet retry loop.

const (
	// maxBodyAttempts caps retries per row. Three tries across three ticks
	// clears anything transient; beyond that the account is the problem.
	maxBodyAttempts = 3
	// DefaultBackfillBatch is how many rows one sweep will fetch.
	DefaultBackfillBatch = 25
)

// BodyBackfiller fills missing message bodies on already-surfaced rows.
type BodyBackfiller struct {
	pool  *pgxpool.Pool
	store *Store
}

func NewBodyBackfiller(pool *pgxpool.Pool) *BodyBackfiller {
	if pool == nil {
		return nil
	}
	return &BodyBackfiller{pool: pool, store: NewStore(pool, nil)}
}

// BackfillResult reports what one sweep did. Every field is a real count, so a
// caller can print an honest line rather than "done".
type BackfillResult struct {
	Scanned int `json:"scanned"`
	Filled  int `json:"filled"`
	Failed  int `json:"failed"`
	// Derived counts rows whose preview / Context pane was filled from a body
	// already stored on the row (no fetch involved).
	Derived int `json:"derived"`
	// Remaining is how many rows still qualify AFTER this sweep, so a caller
	// (or the boss) can see convergence rather than guess at it.
	Remaining int `json:"remaining"`
}

// Run fills up to `limit` rows. A nil fetcher is not an error: without Composio
// wired there is nothing to fetch from, and the sweep reports zero work.
func (b *BodyBackfiller) Run(ctx context.Context, limit int) (BackfillResult, error) {
	var res BackfillResult
	if b == nil || b.pool == nil {
		return res, nil
	}
	// Free pass first: rows that ALREADY hold a message but whose preview or
	// Context pane was left blank need no network call at all, only the same
	// derivation capture does. Runs whether or not a fetcher is installed.
	res.Derived = b.deriveFromStored(ctx)

	f := BodyFetcher()
	if f == nil {
		return res, nil
	}
	if limit <= 0 {
		limit = DefaultBackfillBatch
	}

	rows, err := b.pool.Query(ctx, `
		SELECT id::text, surface, kind, source, COALESCE(external_id,''),
		       COALESCE(body,''), metadata
		  FROM mem_surface_items
		 WHERE surface IN ('followups','inbox','email')
		   AND kind = 'email'
		   AND status = 'open'
		   AND COALESCE(external_id,'') <> ''
		   AND COALESCE(cached_html,'') = ''
		   AND COALESCE(cached_text,'') = ''
		   AND COALESCE((metadata->>'body_fetch_attempts')::int, 0) < $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`, maxBodyAttempts, limit)
	if err != nil {
		return res, fmt.Errorf("surface backfill: query: %w", err)
	}
	type target struct {
		id, surface, kind, source, extID, body string
		meta                                   map[string]any
	}
	var targets []target
	for rows.Next() {
		var t target
		var metaRaw []byte
		if err := rows.Scan(&t.id, &t.surface, &t.kind, &t.source, &t.extID, &t.body, &metaRaw); err != nil {
			continue
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &t.meta)
		}
		if t.meta == nil {
			t.meta = map[string]any{}
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("surface backfill: scan: %w", err)
	}
	res.Scanned = len(targets)

	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		it := &Item{
			ID: t.id, Surface: t.surface, Kind: t.kind, Source: t.source,
			ExternalID: t.extID, Body: t.body, Metadata: t.meta,
		}
		// Reuse the exact capture path Upsert uses, so a backfilled row is
		// indistinguishable from one captured at surface time (same body, same
		// derived preview, same failure stamps). No second implementation.
		b.store.captureBody(ctx, it)
		if strings.TrimSpace(it.CachedHTML) == "" && strings.TrimSpace(it.CachedText) == "" {
			res.Failed++
			b.persistMeta(ctx, it)
			continue
		}
		if err := b.persistBody(ctx, it); err != nil {
			res.Failed++
			log.Printf("surface backfill: persist id=%s: %v", it.ID, err)
			continue
		}
		res.Filled++
	}

	if err := b.pool.QueryRow(ctx, `
		SELECT count(*) FROM mem_surface_items
		 WHERE surface IN ('followups','inbox','email') AND kind='email'
		   AND status='open' AND COALESCE(external_id,'') <> ''
		   AND COALESCE(cached_html,'') = '' AND COALESCE(cached_text,'') = ''
		   AND COALESCE((metadata->>'body_fetch_attempts')::int, 0) < $1
	`, maxBodyAttempts).Scan(&res.Remaining); err != nil {
		res.Remaining = -1 // unknown; never report 0 we didn't measure
	}
	if res.Filled > 0 {
		infoLog.Printf("surface backfill: filled %d of %d message bodies (%d failed, %d still pending)",
			res.Filled, res.Scanned, res.Failed, res.Remaining)
	}
	return res, nil
}

// deriveFromStored fills the SEEN fields (list preview, Context pane) on rows
// that already carry a message body. Purely local: it re-runs the same
// derivation the capture path uses, so a row captured before that derivation
// existed ends up identical to one captured today. Never overwrites text a
// producer wrote.
func (b *BodyBackfiller) deriveFromStored(ctx context.Context) int {
	rows, err := b.pool.Query(ctx, `
		SELECT id::text, COALESCE(body,''), COALESCE(cached_text,''), COALESCE(cached_html,''), metadata
		  FROM mem_surface_items
		 WHERE surface IN ('followups','inbox','email')
		   AND kind = 'email'
		   AND (COALESCE(cached_html,'') <> '' OR COALESCE(cached_text,'') <> '')
		   AND (COALESCE(body,'') = '' OR COALESCE(metadata->>'preview','') = '')
		 LIMIT 500
	`)
	if err != nil {
		log.Printf("surface backfill: derive query: %v", err)
		return 0
	}
	type row struct {
		id, body, text, html string
		meta                 map[string]any
	}
	var targets []row
	for rows.Next() {
		var r row
		var metaRaw []byte
		if err := rows.Scan(&r.id, &r.body, &r.text, &r.html, &metaRaw); err != nil {
			continue
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &r.meta)
		}
		if r.meta == nil {
			r.meta = map[string]any{}
		}
		targets = append(targets, r)
	}
	rows.Close()

	n := 0
	for _, r := range targets {
		if ctx.Err() != nil {
			break
		}
		// Snapshot the previous preview BEFORE deriving: the item shares the
		// metadata map with the scanned row, so comparing the two afterwards
		// compares a value against itself and reports "unchanged" for every
		// row whose body was already set. That silently skipped exactly the
		// rows that needed only a preview.
		prevPreview, _ := r.meta["preview"].(string)
		it := &Item{ID: r.id, Body: r.body, Metadata: r.meta}
		applyBodyDerived(it, r.text, r.html)
		newPreview, _ := it.Metadata["preview"].(string)
		if it.Body == r.body && newPreview == prevPreview {
			continue // nothing derivable (e.g. an empty body already stored)
		}
		metaJSON, err := json.Marshal(it.Metadata)
		if err != nil {
			continue
		}
		if _, err := b.pool.Exec(ctx, `
			UPDATE mem_surface_items
			   SET body       = CASE WHEN COALESCE(body,'') = '' THEN $1 ELSE body END,
			       metadata   = $2::jsonb,
			       updated_at = NOW()
			 WHERE id::text = $3
		`, it.Body, string(metaJSON), it.ID); err != nil {
			log.Printf("surface backfill: derive id=%s: %v", it.ID, err)
			continue
		}
		n++
	}
	if n > 0 {
		infoLog.Printf("surface backfill: derived preview/context for %d stored messages", n)
	}
	return n
}

// persistBody writes the captured body and the fields derived from it. The
// derived preview/body only ever fill a blank; a producer-authored value stands.
func (b *BodyBackfiller) persistBody(ctx context.Context, it *Item) error {
	metaJSON, err := json.Marshal(it.Metadata)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, `
		UPDATE mem_surface_items
		   SET cached_html = $1,
		       cached_text = $2,
		       body        = CASE WHEN COALESCE(body,'') = '' THEN $3 ELSE body END,
		       metadata    = $4::jsonb,
		       updated_at  = NOW()
		 WHERE id::text = $5
	`, it.CachedHTML, it.CachedText, it.Body, string(metaJSON), it.ID)
	return err
}

// persistMeta records a failed attempt so the next sweep can skip a row that
// has exhausted its tries, and so the reason stays readable on the row.
func (b *BodyBackfiller) persistMeta(ctx context.Context, it *Item) {
	metaJSON, err := json.Marshal(it.Metadata)
	if err != nil {
		return
	}
	if _, err := b.pool.Exec(ctx,
		`UPDATE mem_surface_items SET metadata = $1::jsonb, updated_at = NOW() WHERE id::text = $2`,
		string(metaJSON), it.ID,
	); err != nil {
		log.Printf("surface backfill: stamp id=%s: %v", it.ID, err)
	}
}

// StartBodyBackfill runs the sweep on a ticker for the life of the process.
// This is the LIVE PATH for the mechanic: without it the backfiller is code
// nothing calls, which is the same as not having written it.
func StartBodyBackfill(ctx context.Context, pool *pgxpool.Pool, every time.Duration, batch int) {
	b := NewBodyBackfiller(pool)
	if b == nil {
		return
	}
	if every <= 0 {
		every = 5 * time.Minute
	}
	go func() {
		// A short delay lets boot finish wiring the fetcher before the first
		// sweep, so the first tick isn't a guaranteed no-op.
		t := time.NewTimer(45 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if _, err := b.Run(ctx, batch); err != nil {
				log.Printf("surface backfill: %v", err)
			}
			t.Reset(every)
		}
	}()
}
