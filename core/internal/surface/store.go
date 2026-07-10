package surface

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence boundary for the generic surface contract. It
// is the ONLY thing that touches mem_surface_items - the surface_item /
// surface_update tools call through here, never raw SQL.
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

const itemCols = `id::text, surface, kind, source, COALESCE(external_id,''),
	title, subtitle, body, COALESCE(url,''), importance, importance_reason,
	metadata, status, snoozed_until, expires_at, created_at, updated_at, scored_at,
	COALESCE(actions, '[]'::jsonb)`

// Upsert writes an Item. When ExternalID is set it upserts on
// (source, external_id) so a producer re-running its recipe refreshes the
// row instead of duplicating it. Returns the row id and sets it on the
// passed Item.
func (s *Store) Upsert(ctx context.Context, it *Item) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("surface: no pool")
	}
	if it == nil {
		return "", errors.New("surface: nil item")
	}
	it.Surface = strings.TrimSpace(it.Surface)
	it.Title = strings.TrimSpace(it.Title)
	if it.Surface == "" {
		return "", errors.New("surface: surface is required")
	}
	if it.Title == "" {
		return "", errors.New("surface: title is required")
	}
	if it.Kind == "" {
		it.Kind = "item"
	}
	if it.Source == "" {
		it.Source = "agent"
	}
	if it.Status == "" {
		it.Status = StatusOpen
	}
	if !it.Status.Valid() {
		return "", fmt.Errorf("surface: invalid status %q", it.Status)
	}

	// Reconnect-proof dedup: a Gmail message's stable identity is its message
	// id, NOT the Composio connected_account it was fetched through. A revoke+
	// reconnect mints a fresh account id, so keying the row on
	// gmail:<account>:<msgid> would resurface the SAME email as a duplicate
	// (and a quiet inbox would look un-scanned). Canonicalize any 3-part gmail
	// external_id to gmail:<msgid> and keep the account id in metadata for
	// provenance. Done in the store so it holds no matter which recipe wrote it.
	if acct, msgID, ok := splitGmailExternalID(it.ExternalID); ok {
		it.ExternalID = "gmail:" + msgID
		if it.Metadata == nil {
			it.Metadata = map[string]any{}
		}
		if _, exists := it.Metadata["account"]; !exists && acct != "" {
			it.Metadata["account"] = acct
		}
	}

	// Operational notes on the 'system' surface have no natural external_id, so
	// without help every run INSERTs a fresh row — identical "status: ok" cards
	// stack up and become noise. Synthesize a stable id from the title so
	// repeats UPSERT in place instead. Only 'system' is affected; user-facing
	// surfaces (followups/inbox/…) keep their own keys, and any producer that
	// already passes an external_id is untouched.
	if it.ExternalID == "" && it.Surface == "system" {
		sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(it.Title))))
		it.ExternalID = "sys:" + hex.EncodeToString(sum[:8])
	}

	if it.Importance != nil {
		if *it.Importance < 0 {
			*it.Importance = 0
		}
		if *it.Importance > 100 {
			*it.Importance = 100
		}
	}
	meta := it.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("surface: marshal metadata: %w", err)
	}
	actions := it.Actions
	if actions == nil {
		actions = []Action{}
	}
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		return "", fmt.Errorf("surface: marshal actions: %w", err)
	}
	applyDefaultTTL(it)
	var scored *time.Time
	if it.Importance != nil {
		now := time.Now().UTC()
		scored = &now
	}

	// External-id rows upsert; one-offs (no external id) always insert.
	if it.ExternalID != "" {
		var id string
		err = s.pool.QueryRow(ctx, `
			INSERT INTO mem_surface_items
			  (surface, kind, source, external_id, title, subtitle, body, url,
			   importance, importance_reason, metadata, status, snoozed_until,
			   expires_at, scored_at, cached_html, cached_text, actions)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16,$17,$18::jsonb)
			ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL
			DO UPDATE SET
			  surface           = EXCLUDED.surface,
			  kind              = EXCLUDED.kind,
			  title             = EXCLUDED.title,
			  subtitle          = EXCLUDED.subtitle,
			  body              = EXCLUDED.body,
			  url               = EXCLUDED.url,
			  -- Rolling items (Reopen=true): a fresh run's outcome reopens a row
			  -- the boss dismissed for a PREVIOUS outcome. Everything else keeps
			  -- its status untouched, exactly as before.
			  status            = CASE WHEN $19::boolean AND mem_surface_items.status = 'dismissed'
			                           THEN 'open'
			                           ELSE mem_surface_items.status END,
			  importance        = COALESCE(EXCLUDED.importance, mem_surface_items.importance),
			  importance_reason = CASE WHEN EXCLUDED.importance IS NOT NULL
			                           THEN EXCLUDED.importance_reason
			                           ELSE mem_surface_items.importance_reason END,
			  metadata          = EXCLUDED.metadata,
			  expires_at        = EXCLUDED.expires_at,
			  scored_at         = COALESCE(EXCLUDED.scored_at, mem_surface_items.scored_at),
			  -- Keep a captured body across re-poll: only overwrite when the new
			  -- run actually carries one, so a later body-less refresh never
			  -- wipes a durable email we already stored.
			  cached_html       = COALESCE(NULLIF(EXCLUDED.cached_html, ''), mem_surface_items.cached_html),
			  cached_text       = COALESCE(NULLIF(EXCLUDED.cached_text, ''), mem_surface_items.cached_text),
			  -- Refresh actions on re-run only when the producer supplied a
			  -- non-empty set, so a later body-only refresh never wipes them.
			  actions           = CASE WHEN EXCLUDED.actions = '[]'::jsonb
			                           THEN mem_surface_items.actions
			                           ELSE EXCLUDED.actions END,
			  updated_at        = NOW()
			RETURNING id::text
		`, it.Surface, it.Kind, it.Source, it.ExternalID, it.Title, it.Subtitle,
			it.Body, nullStr(it.URL), it.Importance, it.ImportanceReason,
			string(metaJSON), string(it.Status), it.SnoozedUntil, it.ExpiresAt, scored,
			it.CachedHTML, it.CachedText, string(actionsJSON), it.Reopen).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("surface: upsert: %w", err)
		}
		it.ID = id
		return id, nil
	}

	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mem_surface_items
		  (surface, kind, source, title, subtitle, body, url, importance,
		   importance_reason, metadata, status, snoozed_until, expires_at, scored_at,
		   cached_html, cached_text, actions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15,$16,$17::jsonb)
		RETURNING id::text
	`, it.Surface, it.Kind, it.Source, it.Title, it.Subtitle, it.Body,
		nullStr(it.URL), it.Importance, it.ImportanceReason, string(metaJSON),
		string(it.Status), it.SnoozedUntil, it.ExpiresAt, scored,
		it.CachedHTML, it.CachedText, string(actionsJSON)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("surface: insert: %w", err)
	}
	it.ID = id
	return id, nil
}

// Update applies a partial patch to one item. Nil patch fields are left
// untouched. Setting Importance stamps scored_at; setting Status to a
// terminal state stamps decided_at.
func (s *Store) Update(ctx context.Context, id string, p Patch) error {
	if s == nil || s.pool == nil {
		return errors.New("surface: no pool")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("surface: id is required")
	}
	var status *string
	if p.Status != nil {
		if !p.Status.Valid() {
			return fmt.Errorf("surface: invalid status %q", *p.Status)
		}
		v := string(*p.Status)
		status = &v
	}
	if p.Importance != nil {
		if *p.Importance < 0 {
			*p.Importance = 0
		}
		if *p.Importance > 100 {
			*p.Importance = 100
		}
	}
	metaJSON := "{}"
	if p.MetadataMerge != nil {
		b, err := json.Marshal(p.MetadataMerge)
		if err != nil {
			return fmt.Errorf("surface: marshal metadata patch: %w", err)
		}
		metaJSON = string(b)
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_surface_items SET
		  status            = COALESCE($2, status),
		  importance        = COALESCE($3, importance),
		  importance_reason = COALESCE($4, importance_reason),
		  snoozed_until     = COALESCE($5, snoozed_until),
		  metadata          = COALESCE(metadata, '{}'::jsonb) || $6::jsonb,
		  scored_at         = CASE WHEN $3 IS NOT NULL THEN NOW() ELSE scored_at END,
		  decided_at        = CASE WHEN $2 IN ('done','dismissed') THEN NOW() ELSE decided_at END,
		  updated_at        = NOW()
		WHERE id = $1::uuid
	`, id, status, p.Importance, p.ImportanceReason, p.SnoozedUntil, metaJSON)
	if err != nil {
		return fmt.Errorf("surface: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("surface: no item with id %s", id)
	}
	return nil
}

// ListBySurface returns visible items for one surface, ranked. snoozed
// rows whose timer has elapsed are treated as open.
func (s *Store) ListBySurface(ctx context.Context, surface string, limit int) ([]*Item, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+itemCols+`
		  FROM mem_surface_items
		 WHERE surface = $1
		   AND (status = 'open' OR (status = 'snoozed' AND snoozed_until < NOW()))
		 ORDER BY importance DESC NULLS LAST, created_at DESC
		 LIMIT $2
	`, surface, limit)
	if err != nil {
		return nil, fmt.Errorf("surface: list by surface: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

// ListOpen returns every visible item across all surfaces, ordered by
// surface then rank. The dashboard aggregate groups the result by Surface.
func (s *Store) ListOpen(ctx context.Context, limit int) ([]*Item, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+itemCols+`
		  FROM mem_surface_items
		 WHERE status = 'open' OR (status = 'snoozed' AND snoozed_until < NOW())
		 ORDER BY surface, importance DESC NULLS LAST, created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("surface: list open: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

// Get returns one item by id, or (nil, nil) if it doesn't exist.
func (s *Store) Get(ctx context.Context, id string) (*Item, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	row := s.pool.QueryRow(ctx, `SELECT `+itemCols+` FROM mem_surface_items WHERE id = $1::uuid`, id)
	it, err := scanItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

// defaultInfoTTL is how long an informational inbox card lives before it
// self-clears. Three days: long enough that the boss sees it across a weekend,
// short enough that the inbox never becomes an archive. Deliberately longer
// than the 36h the cron/self-heal producers stamp on their own rows — those
// know they're transient; an agent-authored FYI might not be.
const defaultInfoTTL = 72 * time.Hour

// bossOwnedSurfaces never receive an automatic TTL. Follow-up mail is the
// boss's own inbox (nothing automated resolves it), and 'system' is the
// Activity stream, which reads status='open' — expiring those would silently
// erase his activity history rather than tidy his inbox.
var bossOwnedSurfaces = map[string]struct{}{
	"followups": {}, "inbox": {}, "email": {}, "system": {},
}

// applyDefaultTTL gives action-less informational cards an expiry so they can't
// accumulate forever in "Surfaced by Jarvis".
//
// This is a MECHANIC, so it lives here at the single write chokepoint rather
// than as a sentence in a skill telling the agent to "remember to set
// expires_at" — an instruction the runtime LLM drops routinely. Every producer
// (surface_item tool, routine miner, cron outcomes, self-heal) flows through
// Upsert, so all of them inherit it by construction.
//
// Three carve-outs, in order:
//   - An explicit ExpiresAt from the producer always wins.
//   - A card carrying actions is a DECISION, not an FYI. It waits for the boss
//     however long that takes; auto-dismissing it would be the system deciding
//     on his behalf.
//   - Boss-owned surfaces (see above) are never touched.
//
// This does NOT hide errors, and the reason is load-bearing: a persistent
// condition is re-upserted by its producer under a stable external_id (the
// extension/connector/health checklists all do this), which refreshes the TTL
// on every detection. A card therefore only expires once its producer has
// STOPPED reporting the problem — i.e. it resolved. An alert whose producer
// emits once and never again is the one shape this can drop, so producers of
// durable alerts must carry an external_id or an action.
func applyDefaultTTL(it *Item) {
	if it == nil || it.ExpiresAt != nil || len(it.Actions) > 0 {
		return
	}
	if _, boss := bossOwnedSurfaces[it.Surface]; boss {
		return
	}
	exp := time.Now().UTC().Add(defaultInfoTTL)
	it.ExpiresAt = &exp
}

// SweepExpired dismisses open items whose TTL has passed. Returns the
// count dismissed. Called by the nightly consolidate job.
func (s *Store) SweepExpired(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	// Follow-up emails are boss-owned and NEVER auto-expire - excluding them
	// here is the second half of the "nothing automated resolves the boss's
	// inbox" guarantee (the first half lives in the surface_update /
	// followup_dismiss tool guards). A follow-up sits until the boss acts.
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_surface_items
		   SET status = 'dismissed', decided_at = NOW(), updated_at = NOW()
		 WHERE status = 'open' AND expires_at IS NOT NULL AND expires_at < NOW()
		   AND NOT (surface = ANY(ARRAY['followups','inbox','email']) AND kind = 'email')
	`)
	if err != nil {
		return 0, fmt.Errorf("surface: sweep expired: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

func collectItems(rows pgx.Rows) ([]*Item, error) {
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanItem(row pgx.Row) (*Item, error) {
	var (
		it         Item
		imp        *int16
		metaRaw    []byte
		statusS    string
		actionsRaw []byte
	)
	if err := row.Scan(
		&it.ID, &it.Surface, &it.Kind, &it.Source, &it.ExternalID, &it.Title,
		&it.Subtitle, &it.Body, &it.URL, &imp, &it.ImportanceReason, &metaRaw,
		&statusS, &it.SnoozedUntil, &it.ExpiresAt, &it.CreatedAt, &it.UpdatedAt,
		&it.ScoredAt, &actionsRaw,
	); err != nil {
		return nil, err
	}
	if imp != nil {
		v := int(*imp)
		it.Importance = &v
	}
	it.Status = Status(statusS)
	if len(metaRaw) > 0 {
		_ = json.Unmarshal(metaRaw, &it.Metadata)
	}
	if it.Metadata == nil {
		it.Metadata = map[string]any{}
	}
	if len(actionsRaw) > 0 {
		_ = json.Unmarshal(actionsRaw, &it.Actions)
	}
	return &it, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// splitGmailExternalID parses a legacy gmail:<account>:<messageId> external id
// into (account, messageId, true). It returns ok=false for an already-canonical
// gmail:<messageId> (2 parts), for non-gmail ids, or when the message id is
// empty. This is what makes surface dedup survive a mailbox revoke+reconnect:
// the account id (middle segment) changes, the message id does not.
func splitGmailExternalID(ext string) (account, msgID string, ok bool) {
	if !strings.HasPrefix(ext, "gmail:") {
		return "", "", false
	}
	parts := strings.Split(ext, ":")
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], strings.TrimSpace(parts[2]) != ""
}
