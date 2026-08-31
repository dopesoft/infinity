package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyStore is the Postgres-backed home for vendor API keys the boss pastes
// into Studio (mem_provider_keys, migration 198). It exists so adding a brain
// is a paste, not a deploy: BuildRegistry consults it alongside the env, and
// the Settings PUT re-registers the provider in the live registry on save.
//
// Precedence is deliberate and one-directional: a key stored here WINS over
// the matching env var. Typing a key in the UI is the most recent explicit
// instruction; an env var is the deploy-time default.
//
// Nil-safe throughout - a nil store (tests, doctor, no pool) behaves as
// "no keys stored" rather than panicking, so every caller can pass one
// unconditionally.
type KeyStore struct {
	pool *pgxpool.Pool
}

func NewKeyStore(pool *pgxpool.Pool) *KeyStore { return &KeyStore{pool: pool} }

// StoredKey is the metadata half of a credential - everything about a key
// EXCEPT the key. Returned by List so the HTTP layer physically cannot leak
// the secret to a browser.
type StoredKey struct {
	Provider  string    `json:"provider"`
	Hint      string    `json:"hint"`
	Label     string    `json:"label"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Get returns the stored key for a provider. ("", false, nil) means no row -
// distinct from an error, so a caller can tell "not configured" from "the DB
// is unreachable" instead of treating a broken lookup as an absent key.
func (s *KeyStore) Get(ctx context.Context, provider string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, nil
	}
	id := canonicalProviderID(provider)
	if id == "" {
		return "", false, nil
	}
	var key string
	err := s.pool.QueryRow(ctx,
		`SELECT api_key FROM mem_provider_keys WHERE provider = $1`, id,
	).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("provider key lookup (%s): %w", id, err)
	}
	key = strings.TrimSpace(key)
	return key, key != "", nil
}

// Set upserts a key. An empty key is rejected rather than silently stored -
// a blank row would read as "configured" everywhere downstream and then fail
// at the first inference call with a confusing 401.
func (s *KeyStore) Set(ctx context.Context, provider, key, label string) error {
	if s == nil || s.pool == nil {
		return errors.New("provider key store not configured (no database pool)")
	}
	id := canonicalProviderID(provider)
	if id == "" {
		return errors.New("provider id is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("api key is empty")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_provider_keys (provider, api_key, label)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider) DO UPDATE
		   SET api_key = EXCLUDED.api_key,
		       label = EXCLUDED.label,
		       updated_at = now()`,
		id, key, strings.TrimSpace(label))
	if err != nil {
		return fmt.Errorf("store provider key (%s): %w", id, err)
	}
	return nil
}

// Delete removes a stored key. Reports whether a row actually went away so
// the caller can answer "removed" vs "there was nothing there" honestly.
func (s *KeyStore) Delete(ctx context.Context, provider string) (bool, error) {
	if s == nil || s.pool == nil {
		return false, nil
	}
	id := canonicalProviderID(provider)
	if id == "" {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM mem_provider_keys WHERE provider = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete provider key (%s): %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// List returns metadata for every stored key, secret excluded.
func (s *KeyStore) List(ctx context.Context) ([]StoredKey, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT provider, api_key, label, updated_at
		  FROM mem_provider_keys
		 ORDER BY provider`)
	if err != nil {
		return nil, fmt.Errorf("list provider keys: %w", err)
	}
	defer rows.Close()
	out := []StoredKey{}
	for rows.Next() {
		var (
			provider, key, label string
			updated              time.Time
		)
		if err := rows.Scan(&provider, &key, &label, &updated); err != nil {
			return nil, fmt.Errorf("scan provider key: %w", err)
		}
		out = append(out, StoredKey{
			Provider:  provider,
			Hint:      MaskKey(key),
			Label:     label,
			UpdatedAt: updated,
		})
	}
	return out, rows.Err()
}

// MaskKey renders a credential as a recognisable-but-useless hint, so the UI
// can show WHICH key is stored without the secret ever crossing the wire.
// Short strings collapse entirely rather than exposing most of themselves.
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

func canonicalProviderID(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
