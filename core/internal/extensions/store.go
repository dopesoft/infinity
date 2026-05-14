package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence boundary for mem_extensions.
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

const extCols = `id::text, name, kind, description, config, enabled, source,
	status, last_error, created_at, updated_at`

// Upsert saves (or replaces) an extension by name.
func (s *Store) Upsert(ctx context.Context, ext *Extension) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("extensions: no pool")
	}
	cfgJSON, err := json.Marshal(ext.Config)
	if err != nil {
		return "", fmt.Errorf("extensions: marshal config: %w", err)
	}
	source := ext.Source
	if source == "" {
		source = "agent"
	}
	status := ext.Status
	if status == "" {
		status = StatusActive
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mem_extensions
		  (name, kind, description, config, enabled, source, status, last_error)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8)
		ON CONFLICT (name) DO UPDATE SET
		  kind        = EXCLUDED.kind,
		  description = EXCLUDED.description,
		  config      = EXCLUDED.config,
		  enabled     = EXCLUDED.enabled,
		  source      = EXCLUDED.source,
		  status      = EXCLUDED.status,
		  last_error  = EXCLUDED.last_error,
		  updated_at  = NOW()
		RETURNING id::text
	`, ext.Name, string(ext.Kind), ext.Description, string(cfgJSON),
		ext.Enabled, source, string(status), ext.LastError).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("extensions: upsert: %w", err)
	}
	ext.ID = id
	return id, nil
}

// GetByName returns one extension, or (nil, nil) when it doesn't exist.
func (s *Store) GetByName(ctx context.Context, name string) (*Extension, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("extensions: no pool")
	}
	ext, err := scanExtension(s.pool.QueryRow(ctx,
		`SELECT `+extCols+` FROM mem_extensions WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ext, err
}

// List returns every extension, newest first.
func (s *Store) List(ctx context.Context) ([]*Extension, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+extCols+` FROM mem_extensions ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExtensions(rows)
}

// ListEnabled returns extensions that should be activated on boot.
func (s *Store) ListEnabled(ctx context.Context) ([]*Extension, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+extCols+` FROM mem_extensions WHERE enabled = TRUE ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExtensions(rows)
}

// SetStatus records an extension's activation outcome.
func (s *Store) SetStatus(ctx context.Context, name string, status Status, lastErr string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_extensions SET status = $2, last_error = $3, updated_at = NOW()
		 WHERE name = $1
	`, name, string(status), lastErr)
	return err
}

// Disable turns an extension off so it won't load on the next boot.
func (s *Store) Disable(ctx context.Context, name string) error {
	if s == nil || s.pool == nil {
		return errors.New("extensions: no pool")
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_extensions
		   SET enabled = FALSE, status = 'disabled', updated_at = NOW()
		 WHERE name = $1
	`, name)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("extensions: no extension named %q", name)
	}
	return nil
}

func collectExtensions(rows pgx.Rows) ([]*Extension, error) {
	var out []*Extension
	for rows.Next() {
		ext, err := scanExtension(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ext)
	}
	return out, rows.Err()
}

func scanExtension(row pgx.Row) (*Extension, error) {
	var (
		ext     Extension
		kind    string
		status  string
		cfgRaw  []byte
	)
	if err := row.Scan(
		&ext.ID, &ext.Name, &kind, &ext.Description, &cfgRaw, &ext.Enabled,
		&ext.Source, &status, &ext.LastError, &ext.CreatedAt, &ext.UpdatedAt,
	); err != nil {
		return nil, err
	}
	ext.Kind = Kind(kind)
	ext.Status = Status(status)
	if len(cfgRaw) > 0 {
		_ = json.Unmarshal(cfgRaw, &ext.Config)
	}
	if ext.Config == nil {
		ext.Config = map[string]any{}
	}
	return &ext, nil
}
