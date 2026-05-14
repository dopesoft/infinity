package surface

import (
	"context"
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
// is the ONLY thing that touches mem_surface_items — the surface_item /
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
	metadata, status, snoozed_until, expires_at, created_at, updated_at, scored_at`

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
			   expires_at, scored_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15)
			ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL
			DO UPDATE SET
			  surface           = EXCLUDED.surface,
			  kind              = EXCLUDED.kind,
			  title             = EXCLUDED.title,
			  subtitle          = EXCLUDED.subtitle,
			  body              = EXCLUDED.body,
			  url               = EXCLUDED.url,
			  importance        = COALESCE(EXCLUDED.importance, mem_surface_items.importance),
			  importance_reason = CASE WHEN EXCLUDED.importance IS NOT NULL
			                           THEN EXCLUDED.importance_reason
			                           ELSE mem_surface_items.importance_reason END,
			  metadata          = EXCLUDED.metadata,
			  expires_at        = EXCLUDED.expires_at,
			  scored_at         = COALESCE(EXCLUDED.scored_at, mem_surface_items.scored_at),
			  updated_at        = NOW()
			RETURNING id::text
		`, it.Surface, it.Kind, it.Source, it.ExternalID, it.Title, it.Subtitle,
			it.Body, nullStr(it.URL), it.Importance, it.ImportanceReason,
			string(metaJSON), string(it.Status), it.SnoozedUntil, it.ExpiresAt, scored).Scan(&id)
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
		   importance_reason, metadata, status, snoozed_until, expires_at, scored_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14)
		RETURNING id::text
	`, it.Surface, it.Kind, it.Source, it.Title, it.Subtitle, it.Body,
		nullStr(it.URL), it.Importance, it.ImportanceReason, string(metaJSON),
		string(it.Status), it.SnoozedUntil, it.ExpiresAt, scored).Scan(&id)
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
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_surface_items SET
		  status            = COALESCE($2, status),
		  importance        = COALESCE($3, importance),
		  importance_reason = COALESCE($4, importance_reason),
		  snoozed_until     = COALESCE($5, snoozed_until),
		  scored_at         = CASE WHEN $3 IS NOT NULL THEN NOW() ELSE scored_at END,
		  decided_at        = CASE WHEN $2 IN ('done','dismissed') THEN NOW() ELSE decided_at END,
		  updated_at        = NOW()
		WHERE id = $1::uuid
	`, id, status, p.Importance, p.ImportanceReason, p.SnoozedUntil)
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

// SweepExpired dismisses open items whose TTL has passed. Returns the
// count dismissed. Called by the nightly consolidate job.
func (s *Store) SweepExpired(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_surface_items
		   SET status = 'dismissed', decided_at = NOW(), updated_at = NOW()
		 WHERE status = 'open' AND expires_at IS NOT NULL AND expires_at < NOW()
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
		it      Item
		imp     *int16
		metaRaw []byte
		statusS string
	)
	if err := row.Scan(
		&it.ID, &it.Surface, &it.Kind, &it.Source, &it.ExternalID, &it.Title,
		&it.Subtitle, &it.Body, &it.URL, &imp, &it.ImportanceReason, &metaRaw,
		&statusS, &it.SnoozedUntil, &it.ExpiresAt, &it.CreatedAt, &it.UpdatedAt,
		&it.ScoredAt,
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
	return &it, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
