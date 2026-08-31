package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The phone vault's secrets used to live as PLAINTEXT rows in infinity_meta:
// the boss's card number, security code, date of birth and account number,
// readable over GET /api/meta by anything holding a session. This is the one
// shot that moves them somewhere they belong.
//
// # WHAT THIS DESTROYS, DELIBERATELY
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

// A non-card secret is stored in the same table under the same key, marked
// TWO ways: a label prefix so it is legible in the database, and a brand the
// card queries filter on (see notASecret in vault.go).
//
// The brand is the load-bearing one. The label prefix alone was what the
// wallet was relying on, and it relied on it by never checking — so the boss's
// spoken passphrase turned up in a list of cards he could pay with.
const (
	secretLabelPrefix = "secret:"
	secretBrand       = "secret"
)

// Keys for the details the phone releases server-side. Named here so the HTTP
// layer and the phone both spell them the same way.
const (
	KeyPassphrase = "vault.phone_passphrase"
	KeyIdentity   = "vault.identity"
	KeyCard       = "vault.payment_card"
	KeyBossCell   = "vault.boss_cell"
)

func (s *Store) putSecret(ctx context.Context, key, value string) error {
	sealed, nonce, err := s.seal(Secrets{PAN: "", CVC: "", Name: value})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_vault_cards (label, brand, last4, sealed, nonce, key_version)
		VALUES ($1, $5, '', $2, $3, $4)
	`, secretLabelPrefix+key, sealed, nonce, keyVersion, secretBrand)
	return err
}

// Has reports whether a secret is stored, WITHOUT opening it. This is what the
// settings screen asks: it can tell the boss a thing is saved without the
// value ever leaving the database, which is the whole reason it was sealed.
func (s *SecretStore) Has(ctx context.Context, key string) (bool, error) {
	if s == nil || s.s == nil || s.s.pool == nil {
		return false, nil
	}
	var n int
	err := s.s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_vault_cards
		 WHERE label = $1 AND revoked_at IS NULL
	`, secretLabelPrefix+key).Scan(&n)
	return n > 0, err
}

// Clear retires a secret. Used when the boss empties a field.
func (s *SecretStore) Clear(ctx context.Context, key string) error {
	if s == nil || s.s == nil || s.s.pool == nil {
		return nil
	}
	_, err := s.s.pool.Exec(ctx,
		`UPDATE mem_vault_cards SET revoked_at = NOW() WHERE label = $1 AND revoked_at IS NULL`,
		secretLabelPrefix+key)
	return err
}

// Releasable is the phone's view of the boss's details: only the ones he has
// switched on. It is a thin pass-through to Details.Release so there is exactly
// one place the switch is enforced.
func (s *SecretStore) Releasable(ctx context.Context) (map[string]string, error) {
	if s == nil || s.s == nil {
		return map[string]string{}, nil
	}
	return NewDetails(s.s).Release(ctx)
}

// Detail reads one detail regardless of its release switch. Used for the
// spoken password, which Jarvis checks and never says.
func (s *SecretStore) Detail(ctx context.Context, key string) (string, error) {
	if s == nil || s.s == nil {
		return "", ErrNoKey
	}
	return NewDetails(s.s).Get(ctx, key)
}

// PhoneCard renders the card a spoken call brief should read out, in the shape
// the phone has always parsed.
//
// It prefers a WALLET card, and that is the point: before this there were two
// separate places to type a card number, one for buying and one for calling,
// and neither knew about the other. One list of cards now serves both. The
// legacy sealed blob is still the fallback, so a boss who has not added a
// wallet card yet keeps exactly the phone behaviour he has today.
func (s *SecretStore) PhoneCard(ctx context.Context) (string, error) {
	if s == nil || s.s == nil {
		return "", ErrNoKey
	}
	if cards, err := s.s.List(ctx); err == nil && len(cards) > 0 {
		// Newest first, per List's ordering.
		if sec, err := s.s.Open(ctx, cards[0].ID); err == nil && sec.PAN != "" {
			out := map[string]string{
				"number": sec.PAN,
				"cvc":    sec.CVC,
				"name":   sec.Name,
				"zip":    sec.Billing["zip"],
				"exp":    sec.Billing["exp"],
			}
			if out["exp"] == "" && cards[0].ExpMonth > 0 && cards[0].ExpYear > 0 {
				out["exp"] = fmt.Sprintf("%02d/%02d", cards[0].ExpMonth, cards[0].ExpYear%100)
			}
			b, err := json.Marshal(out)
			if err == nil {
				return string(b), nil
			}
		}
	}
	return s.Secret(ctx, KeyCard)
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
