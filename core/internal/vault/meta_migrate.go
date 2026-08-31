package vault

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The phone vault's secrets used to live as PLAINTEXT rows in infinity_meta:
// the boss's card number, security code, date of birth and account number,
// readable over GET /api/meta by anything holding a session. This is the one
// shot that moves them somewhere they belong.
//
// WHAT THIS DESTROYS, DELIBERATELY
//
// The plaintext copy. After the move the meta row holds the sentinel below and
// the real value exists only sealed under INFINITY_VAULT_KEY. That is the
// point of the exercise, and it means losing the key means re-entering the
// card. The alternative was leaving a live card number readable over HTTP, so
// this is the right side of the trade, but it IS destructive and is worth
// naming as such.
//
// The phone feature does not change. phone.Manager reads through one helper,
// so repointing that helper moves all four keys at once and the call briefs
// behave exactly as before.
const movedSentinel = "moved:vault"

// SecretMetaKeys are the infinity_meta keys that hold real secrets.
var SecretMetaKeys = []string{
	"vault.payment_card",
	"vault.identity",
	"vault.phone_passphrase",
	// vault.boss_cell is a phone number, not a secret, and the phone book
	// already holds numbers in the clear. Sealing it would buy nothing and
	// would break patch-in when the key is absent.
}

// SecretStore keeps the non-card secrets (identity details, the spoken
// passphrase) sealed under the same key as the cards.
type SecretStore struct{ s *Store }

func NewSecretStore(s *Store) *SecretStore { return &SecretStore{s: s} }

// MigrateFromMeta seals every plaintext secret still sitting in infinity_meta
// and replaces the row's value with a sentinel. Idempotent: a row already
// carrying the sentinel is skipped, so this is safe to run on every boot.
//
// Returns how many it moved. A vault with no key moves nothing and says so
// rather than silently leaving plaintext in place looking migrated.
func MigrateFromMeta(ctx context.Context, pool *pgxpool.Pool, s *Store) (int, error) {
	if pool == nil {
		return 0, nil
	}
	if !s.Healthy() {
		return 0, ErrNoKey
	}
	moved := 0
	for _, key := range SecretMetaKeys {
		var value string
		err := pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, key).Scan(&value)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return moved, err
		}
		value = strings.TrimSpace(value)
		if value == "" || value == movedSentinel {
			continue
		}
		if err := s.putSecret(ctx, key, value); err != nil {
			// Leave the plaintext alone if sealing failed. A row we cannot
			// re-read is worse than a row we have not moved yet.
			return moved, err
		}
		if _, err := pool.Exec(ctx,
			`UPDATE infinity_meta SET value = $2, updated_at = NOW() WHERE key = $1`, key, movedSentinel); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// secretRow is how a non-card secret is stored: same table, same key, marked
// by its label so it never collides with a real card.
const secretLabelPrefix = "secret:"

func (s *Store) putSecret(ctx context.Context, key, value string) error {
	sealed, nonce, err := s.seal(Secrets{PAN: "", CVC: "", Name: value})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_vault_cards (label, brand, last4, sealed, nonce, key_version)
		VALUES ($1, 'secret', '', $2, $3, $4)
	`, secretLabelPrefix+key, sealed, nonce, keyVersion)
	return err
}

// Secret reads back one migrated secret. Used by the phone brief splice, which
// is server-side and never returns the value to the model.
func (s *SecretStore) Secret(ctx context.Context, key string) (string, error) {
	if s == nil || !s.s.Healthy() {
		return "", ErrNoKey
	}
	var sealed, nonce []byte
	err := s.s.pool.QueryRow(ctx, `
		SELECT sealed, nonce FROM mem_vault_cards
		 WHERE label = $1 AND revoked_at IS NULL
		 ORDER BY created_at DESC LIMIT 1
	`, secretLabelPrefix+key).Scan(&sealed, &nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	sec, err := s.s.open(sealed, nonce)
	if err != nil {
		return "", err
	}
	return sec.Name, nil
}

// PutSecret stores or replaces one non-card secret.
func (s *SecretStore) PutSecret(ctx context.Context, key, value string) error {
	if s == nil || !s.s.Healthy() {
		return ErrNoKey
	}
	if _, err := s.s.pool.Exec(ctx,
		`UPDATE mem_vault_cards SET revoked_at = NOW() WHERE label = $1 AND revoked_at IS NULL`,
		secretLabelPrefix+key); err != nil {
		return err
	}
	return s.s.putSecret(ctx, key, value)
}

// PhoneCardText renders a stored card for a spoken call brief, the same shape
// the phone tool has always produced. It lives here so the formatting sits
// next to the decryption and neither ever crosses into the agent's context.
func (s *SecretStore) PhoneCardText(ctx context.Context, raw string) string {
	var c struct {
		Name   string `json:"name"`
		Number string `json:"number"`
		Exp    string `json:"exp"`
		CVC    string `json:"cvc"`
		Zip    string `json:"zip"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil || c.Number == "" {
		return raw // legacy free-text values pass through untouched
	}
	out := "Card number " + c.Number
	if c.Exp != "" {
		out += ", expiry " + c.Exp
	}
	if c.CVC != "" {
		out += ", security code " + c.CVC
	}
	if c.Name != "" {
		out += ", name on card " + c.Name
	}
	if c.Zip != "" {
		out += ", billing zip " + c.Zip
	}
	return out
}
