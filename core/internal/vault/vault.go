// Package vault holds the secrets Jarvis is trusted with but must never see:
// card numbers, security codes, website credentials, identity details.
//
// # THE ONE IDEA
//
// The planning model gets an opaque reference and a human label. It never gets
// the secret. The secret is decrypted inside the narrow piece of code that
// needs it, used, and dropped. This is not a new pattern for Infinity: the
// phone tool has worked this way since it shipped, splicing the card into a
// call brief server-side while the model passes nothing but a boolean
// (core/internal/phone/tools.go). What was missing was storage worth trusting.
// Before this package, those secrets sat as plaintext rows in infinity_meta,
// which GET /api/meta served back to any authenticated caller.
//
// # FAIL CLOSED
//
// With no INFINITY_VAULT_KEY there is no vault, and every path that would use
// one refuses rather than degrading to something weaker. A purchase that
// cannot reach a healthy vault does not fall back to asking the model for a
// card number; it stops. tab.bot documents the same rule for the same reason:
// the failure mode of a payment boundary must be "nothing happened", never
// "something happened differently".
//
// # SWAPPABLE
//
// CardVault is an interface and Store is one implementation of it, so moving to
// a PCI vendor (Basis Theory, VGS, Skyflow) or to per-transaction virtual cards
// later is a change of implementation, not a change of callers. The display
// fields deliberately mirror what such a vendor hands back.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoKey is returned by every operation when INFINITY_VAULT_KEY is unset.
// Callers must treat it as "the vault is unavailable", never as "there is no
// card": the difference between those two is the difference between stopping
// and doing the wrong thing.
var ErrNoKey = errors.New("vault: INFINITY_VAULT_KEY is not set, so nothing can be sealed or opened")

// ErrNotFound means the reference did not name a live card.
var ErrNotFound = errors.New("vault: no such card")

// keyVersion is stamped on every row this build seals. Rotating the key means
// bumping this and keeping the old key readable until rows are re-sealed.
const keyVersion = 1

// Card is the CLEAR half: what a human needs to tell two cards apart, and what
// the agent is allowed to know. There is no field here that helps anyone spend
// money, which is the point.
type Card struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	Brand           string     `json:"brand"`
	Last4           string     `json:"last4"`
	ExpMonth        int        `json:"exp_month,omitempty"`
	ExpYear         int        `json:"exp_year,omitempty"`
	BillingComplete bool       `json:"billing_complete"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

// Secrets is the SEALED half. It exists in memory only, inside the boundary
// that is about to use it. It has no json tags for a reason: nothing should be
// able to serialise this by accident into a log, a payload or an API response.
type Secrets struct {
	PAN     string
	CVC     string
	Name    string
	Billing map[string]string
}

// CardVault is the seam a PCI vendor would replace.
type CardVault interface {
	// Healthy reports whether the vault can actually seal and open right now.
	// Payment paths must check this and refuse rather than degrade.
	Healthy() bool
	List(ctx context.Context) ([]Card, error)
	Get(ctx context.Context, id string) (Card, error)
	// Open is the only way to reach the secrets, and is deliberately not
	// exposed by any tool or HTTP route.
	Open(ctx context.Context, id string) (Secrets, error)
	Put(ctx context.Context, in PutCard) (Card, error)
	Revoke(ctx context.Context, id string) error
}

// PutCard is what the private enrollment page posts. It is accepted at exactly
// one endpoint and never travels through chat.
type PutCard struct {
	Label    string
	PAN      string
	CVC      string
	Name     string
	ExpMonth int
	ExpYear  int
	Billing  map[string]string
}

// Store is the self-hosted implementation: AES-256-GCM under a key from the
// environment, ciphertext in Postgres.
type Store struct {
	pool *pgxpool.Pool
	key  []byte
}

var _ CardVault = (*Store)(nil)

// New builds a Store. A missing or malformed key is not an error here: the
// vault simply reports unhealthy, and callers fail closed with a message that
// says what to set. Refusing to boot would take the whole agent down over a
// feature the boss may not be using yet.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, key: loadKey()}
}

// loadKey reads INFINITY_VAULT_KEY as base64 or hex and requires 32 bytes,
// because AES-256 is the whole point and a short key would silently give less.
func loadKey() []byte {
	raw := strings.TrimSpace(os.Getenv("INFINITY_VAULT_KEY"))
	if raw == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b
	}
	return nil
}

// hasKey reports whether sealing and opening are possible. Crypto does not
// need a database, and conflating the two made seal() refuse whenever the pool
// happened to be absent.
func (s *Store) hasKey() bool { return s != nil && len(s.key) == 32 }

// Healthy reports whether the vault can actually store and retrieve: a key AND
// somewhere to put the ciphertext. This is what payment paths check before they
// commit to a route that will need a card.
func (s *Store) Healthy() bool { return s.hasKey() && s.pool != nil }

func (s *Store) seal(sec Secrets) (ciphertext, nonce []byte, err error) {
	if !s.hasKey() {
		return nil, nil, ErrNoKey
	}
	plain, err := json.Marshal(map[string]any{
		"pan": sec.PAN, "cvc": sec.CVC, "name": sec.Name, "billing": sec.Billing,
	})
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plain, nil), nonce, nil
}

func (s *Store) open(ciphertext, nonce []byte) (Secrets, error) {
	var out Secrets
	if !s.hasKey() {
		return out, ErrNoKey
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return out, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return out, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM is authenticated, so this also fires on tampering, not just on
		// the wrong key. Either way the honest answer is that we cannot read
		// it, never a partial or guessed result.
		return out, fmt.Errorf("vault: could not open the sealed card (wrong key, or the row was altered): %w", err)
	}
	var raw struct {
		PAN     string            `json:"pan"`
		CVC     string            `json:"cvc"`
		Name    string            `json:"name"`
		Billing map[string]string `json:"billing"`
	}
	if err := json.Unmarshal(plain, &raw); err != nil {
		return out, err
	}
	return Secrets{PAN: raw.PAN, CVC: raw.CVC, Name: raw.Name, Billing: raw.Billing}, nil
}

// Put seals a card and returns only its clear half.
func (s *Store) Put(ctx context.Context, in PutCard) (Card, error) {
	var out Card
	if !s.Healthy() {
		return out, ErrNoKey
	}
	pan := digitsOnly(in.PAN)
	if len(pan) < 13 || len(pan) > 19 {
		return out, errors.New("vault: that does not look like a card number")
	}
	sealed, nonce, err := s.seal(Secrets{PAN: pan, CVC: strings.TrimSpace(in.CVC), Name: in.Name, Billing: in.Billing})
	if err != nil {
		return out, err
	}
	id := uuid.NewString()
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = brandOf(pan) + " ending " + last4(pan)
	}
	billingComplete := len(in.Billing) > 0
	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_vault_cards
		  (id, label, brand, last4, exp_month, exp_year, billing_complete, sealed, nonce, key_version)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, label, brandOf(pan), last4(pan), nullableInt(in.ExpMonth), nullableInt(in.ExpYear),
		billingComplete, sealed, nonce, keyVersion)
	if err != nil {
		return out, err
	}
	return Card{
		ID: id, Label: label, Brand: brandOf(pan), Last4: last4(pan),
		ExpMonth: in.ExpMonth, ExpYear: in.ExpYear, BillingComplete: billingComplete,
		CreatedAt: time.Now(),
	}, nil
}

const cardCols = `id::text, label, brand, last4,
       COALESCE(exp_month, 0), COALESCE(exp_year, 0), billing_complete,
       created_at, last_used_at`

// notASecret excludes the non-card secrets that share this table (the spoken
// passphrase, the identity details) from everything that treats a row as a
// card.
//
// They live here because they want the same key and the same ciphertext
// column, but they are NOT cards, and a table that holds both will hand a
// passphrase to anything asking for a card unless every card query says so.
// It did exactly that once: the boss opened his wallet and found
// "secret:vault.phone_passphrase" sitting in a list captioned "cards Jarvis
// can pay with". Filtering at the three card reads is what makes that
// impossible rather than merely unlikely.
const notASecret = ` AND brand <> '` + secretBrand + `'`

func (s *Store) List(ctx context.Context) ([]Card, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+cardCols+`
		  FROM mem_vault_cards
		 WHERE revoked_at IS NULL`+notASecret+`
		 ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (Card, error) {
	var out Card
	if s == nil || s.pool == nil || strings.TrimSpace(id) == "" {
		return out, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `
		SELECT `+cardCols+`
		  FROM mem_vault_cards
		 WHERE id = $1::uuid AND revoked_at IS NULL`+notASecret+`
	`, id)
	c, err := scanCard(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	return c, nil
}

// Open decrypts a card. Called ONLY from inside a fill boundary. There is no
// tool and no HTTP route that reaches this, and adding one would defeat the
// entire design.
func (s *Store) Open(ctx context.Context, id string) (Secrets, error) {
	var out Secrets
	if !s.Healthy() {
		return out, ErrNoKey
	}
	var sealed, nonce []byte
	var ver int
	err := s.pool.QueryRow(ctx, `
		SELECT sealed, nonce, key_version
		  FROM mem_vault_cards
		 WHERE id = $1::uuid AND revoked_at IS NULL`+notASecret+`
	`, id).Scan(&sealed, &nonce, &ver)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	if ver != keyVersion {
		return out, fmt.Errorf("vault: card is sealed under key version %d, this build carries %d", ver, keyVersion)
	}
	sec, err := s.open(sealed, nonce)
	if err != nil {
		return out, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE mem_vault_cards SET last_used_at = NOW() WHERE id = $1::uuid`, id)
	return sec, nil
}

func (s *Store) Revoke(ctx context.Context, id string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE mem_vault_cards SET revoked_at = NOW() WHERE id = $1::uuid AND revoked_at IS NULL`+notASecret, id)
	return err
}

// ── enrollment links ──────────────────────────────────────────────────────

// EnrollTTL is how long a card-entry link is worth anything. Short on purpose:
// the link is the one moment a card is in flight.
const EnrollTTL = 10 * time.Minute

// NewEnrollment mints a single-use link token. The token is returned to the
// caller once and stored only as a hash, so a leaked database cannot mint a
// working link.
func (s *Store) NewEnrollment(ctx context.Context) (token string, expires time.Time, err error) {
	if !s.Healthy() {
		return "", time.Time{}, ErrNoKey
	}
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", time.Time{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	expires = time.Now().Add(EnrollTTL)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_vault_enrollments (token_hash, expires_at) VALUES ($1, $2)
	`, hashToken(token), expires)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// RedeemEnrollment consumes a token, returning an error if it is unknown,
// expired, or already used. Single-use is enforced in SQL so two simultaneous
// posts cannot both win.
func (s *Store) RedeemEnrollment(ctx context.Context, token string) error {
	if !s.Healthy() {
		return ErrNoKey
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_vault_enrollments
		   SET used_at = NOW()
		 WHERE token_hash = $1
		   AND used_at IS NULL
		   AND expires_at > NOW()
		 RETURNING id::text
	`, hashToken(token)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("that card link has expired or was already used, so I minted nothing with it")
		}
		return err
	}
	return nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// ── helpers ───────────────────────────────────────────────────────────────

type scannable interface{ Scan(dest ...any) error }

func scanCard(r scannable) (Card, error) {
	var (
		c                 Card
		expMonth, expYear int
		lastUsed          *time.Time
	)
	err := r.Scan(&c.ID, &c.Label, &c.Brand, &c.Last4, &expMonth, &expYear,
		&c.BillingComplete, &c.CreatedAt, &lastUsed)
	if err != nil {
		return c, err
	}
	c.ExpMonth, c.ExpYear, c.LastUsedAt = expMonth, expYear, lastUsed
	return c, nil
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func last4(pan string) string {
	if len(pan) < 4 {
		return ""
	}
	return pan[len(pan)-4:]
}

// brandOf is display only. It never gates anything, so a wrong guess costs a
// label and nothing else.
func brandOf(pan string) string {
	switch {
	case strings.HasPrefix(pan, "4"):
		return "Visa"
	case len(pan) > 1 && pan[0] == '5' && pan[1] >= '1' && pan[1] <= '5':
		return "Mastercard"
	case strings.HasPrefix(pan, "34"), strings.HasPrefix(pan, "37"):
		return "Amex"
	case strings.HasPrefix(pan, "6"):
		return "Discover"
	default:
		return "Card"
	}
}

func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
