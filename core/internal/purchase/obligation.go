// Package purchase turns "buy this" into something that can only happen once,
// only on the terms the boss agreed to, and never silently.
//
// # THE PROBLEM IT REPLACES
//
// Approval used to attach to a CLICK. BrowserGate matched the label the model
// volunteered on a browser_act against a list of words like "buy" and
// "checkout", so a button reading "Continue" went through unattended, and an
// approval granted for a $40 cart still fired if the cart had become $400 by
// the time it ran. Afterwards nothing checked whether the order existed.
//
// # AN OBLIGATION IS THE MISSING NOUN
//
// It binds every term at once: merchant, cart, currency, exact total,
// recipient, the origins allowed to receive the card, the browser session, the
// card reference and a deadline. The boss approves THAT, not a click. The fill
// boundary re-checks it twice, once before claiming and once against the live
// page immediately before submitting, so a cart that moved during the approval
// wait invalidates the agreement instead of riding on it.
//
// # WHY THE ORDER OF WRITES MATTERS MORE THAN THE CHECKS
//
// Every check here can be defeated by a crash at the wrong moment; the write
// order cannot. `submitted` is stamped BEFORE the charge-bearing click, so a
// process that dies mid-click leaves evidence that a charge may exist. That
// resolves to `uncertain`, which is never retried automatically. The inverse
// design (stamp after) makes a crash indistinguishable from "never happened",
// and the repair for "never happened" is a second charge.
package purchase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values. The set is closed and mirrored by a CHECK constraint in
// migration 201, so an unknown status cannot reach the table.
const (
	StatusDraft       = "draft"
	StatusPending     = "pending_approval"
	StatusApproved    = "approved"
	StatusClaimed     = "claimed"
	StatusSubmitted   = "submitted"
	StatusAwaiting3DS = "awaiting_3ds"
	StatusConfirmed   = "confirmed"
	StatusUncertain   = "uncertain"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusExpired     = "expired"
)

// InEnglish turns a status into the sentence the boss actually reads.
//
// The raw values above are storage. They are `pending_approval` and
// `awaiting_3ds` because a CHECK constraint and a state machine need stable
// tokens, and NONE of them should ever reach a screen or a chat message — see
// "Plain English in the UI" in CLAUDE.md. This is the one place that
// translation lives, so a new status is a line here and every surface picks it
// up rather than three of them rendering the token.
func InEnglish(status string) string {
	switch status {
	case StatusDraft:
		return "being put together"
	case StatusPending:
		return "waiting for you to approve it"
	case StatusApproved:
		return "approved, about to be paid"
	case StatusClaimed:
		return "being paid right now"
	case StatusSubmitted:
		return "paid, waiting on the shop to confirm"
	case StatusAwaiting3DS:
		return "your bank wants to check it is you"
	case StatusConfirmed:
		return "done, with an order number"
	case StatusUncertain:
		return "sent, but I could not confirm it, and I have not tried again"
	case StatusFailed:
		return "it did not go through"
	case StatusCancelled:
		return "cancelled"
	case StatusExpired:
		return "it sat too long and expired"
	default:
		return status
	}
}

// allowed is the state machine, written out rather than inferred, because the
// transitions that are ABSENT are the safety property. Nothing returns to
// approved, and nothing leaves submitted except a resolution.
var allowed = map[string][]string{
	StatusDraft:       {StatusPending, StatusCancelled, StatusExpired},
	StatusPending:     {StatusApproved, StatusCancelled, StatusExpired, StatusFailed},
	StatusApproved:    {StatusClaimed, StatusCancelled, StatusExpired},
	StatusClaimed:     {StatusSubmitted, StatusAwaiting3DS, StatusCancelled, StatusFailed},
	StatusAwaiting3DS: {StatusSubmitted, StatusFailed, StatusCancelled, StatusExpired},
	StatusSubmitted:   {StatusConfirmed, StatusUncertain, StatusFailed},
	// Terminal.
	StatusConfirmed: {},
	StatusUncertain: {},
	StatusFailed:    {},
	StatusCancelled: {},
	StatusExpired:   {},
}

// CanTransition reports whether a status change is legal.
func CanTransition(from, to string) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ErrNotClaimable is what a losing claimant gets. It is deliberately a named
// error rather than a generic failure, because the correct response is to stop
// and report, never to retry.
var ErrNotClaimable = errors.New("this purchase was already claimed, so I did not start it a second time")

// ErrNotFound means no such obligation.
var ErrNotFound = errors.New("no such purchase")

// LineItem is one thing in the cart. Kept small and comparable because it
// feeds the fingerprint.
type LineItem struct {
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	UnitCents int64  `json:"unit_cents"`
	URL       string `json:"url,omitempty"`
	VariantID string `json:"variant_id,omitempty"`
}

// Obligation is the binding record.
type Obligation struct {
	ID              string         `json:"id"`
	SessionID       string         `json:"session_id"`
	TrustContractID string         `json:"trust_contract_id,omitempty"`
	MerchantHost    string         `json:"merchant_host"`
	MerchantName    string         `json:"merchant_name"`
	Cart            []LineItem     `json:"cart"`
	Currency        string         `json:"currency"`
	TotalCents      int64          `json:"total_cents"`
	Recipient       map[string]any `json:"recipient"`
	PaymentOrigins  []string       `json:"payment_origins"`
	BrowserSession  string         `json:"browser_session_id"`
	CardID          string         `json:"card_id,omitempty"`
	Status          string         `json:"status"`
	IdempotencyKey  string         `json:"idempotency_key"`
	ExpiresAt       time.Time      `json:"expires_at"`
	ClaimedAt       *time.Time     `json:"claimed_at,omitempty"`
	SubmittedAt     *time.Time     `json:"submitted_at,omitempty"`
	ConfirmedAt     *time.Time     `json:"confirmed_at,omitempty"`
	Confirmation    map[string]any `json:"confirmation"`
	Failure         map[string]any `json:"failure"`
	CreatedAt       time.Time      `json:"created_at"`
}

// Total renders the amount the way a human reads it, which is what goes on the
// approval card. Two carts from one merchant otherwise look identical.
func (o *Obligation) Total() string {
	return fmt.Sprintf("%s %.2f", strings.ToUpper(o.Currency), float64(o.TotalCents)/100)
}

// Fingerprint is DETERMINISTIC over the contents of the purchase.
//
// A random idempotency key would let a second propose for the same cart mint a
// second approvable obligation, and the boss would see two cards that look the
// same and approve both. Deriving the key from what is being bought means the
// same cart is always the same obligation, so a duplicate propose returns the
// existing one instead of creating a rival.
func Fingerprint(merchantHost, currency string, totalCents int64, cart []LineItem) string {
	items := make([]string, 0, len(cart))
	for _, li := range cart {
		items = append(items, fmt.Sprintf("%s|%d|%d|%s",
			strings.ToLower(strings.TrimSpace(li.Title)), li.Quantity, li.UnitCents, li.VariantID))
	}
	sort.Strings(items) // cart order is not part of the identity of a purchase
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%d\n%s",
		strings.ToLower(strings.TrimSpace(merchantHost)),
		strings.ToUpper(strings.TrimSpace(currency)),
		totalCents,
		strings.Join(items, "\n"))
	return hex.EncodeToString(h.Sum(nil))
}

// Store is the only writer of mem_purchase_obligations. Every status change
// goes through it so the state machine cannot be bypassed.
type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

const obligationCols = `id::text, session_id, COALESCE(trust_contract_id::text,''),
       merchant_host, merchant_name, cart, currency, total_cents, recipient,
       payment_origins, browser_session_id, COALESCE(card_id::text,''), status,
       idempotency_key, expires_at, claimed_at, submitted_at, confirmed_at,
       confirmation, failure, created_at`

// Propose creates an obligation, or returns the LIVE one that already covers
// this exact cart. It never creates a rival for the same purchase.
func (s *Store) Propose(ctx context.Context, o *Obligation) (*Obligation, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, errors.New("purchase: no database")
	}
	if err := validate(o); err != nil {
		return nil, false, err
	}
	o.IdempotencyKey = Fingerprint(o.MerchantHost, o.Currency, o.TotalCents, o.Cart)

	if existing, err := s.byFingerprint(ctx, o.IdempotencyKey); err == nil && existing != nil {
		return existing, false, nil
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.Status == "" {
		o.Status = StatusDraft
	}
	if o.ExpiresAt.IsZero() {
		o.ExpiresAt = time.Now().Add(DefaultTTL)
	}
	cart, _ := json.Marshal(o.Cart)
	recipient, _ := json.Marshal(orEmpty(o.Recipient))
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_purchase_obligations
		  (id, session_id, merchant_host, merchant_name, cart, currency, total_cents,
		   recipient, payment_origins, browser_session_id, card_id, status,
		   idempotency_key, expires_at)
		VALUES ($1::uuid,$2,$3,$4,$5::jsonb,$6,$7,$8::jsonb,$9,$10,
		        NULLIF($11,'')::uuid,$12,$13,$14)
	`, o.ID, o.SessionID, o.MerchantHost, o.MerchantName, cart, strings.ToUpper(o.Currency),
		o.TotalCents, recipient, o.PaymentOrigins, o.BrowserSession, o.CardID,
		o.Status, o.IdempotencyKey, o.ExpiresAt)
	if err != nil {
		// The unique index can still fire under a genuine race; losing it is
		// the correct outcome, so hand back the winner rather than an error.
		if existing, e2 := s.byFingerprint(ctx, o.IdempotencyKey); e2 == nil && existing != nil {
			return existing, false, nil
		}
		return nil, false, err
	}
	return o, true, nil
}

// DefaultTTL bounds how long an unapproved purchase stays live. It is
// deliberately shorter than the browser session's idle reap (35m in
// browser.Registry) so an approval never lands on a session that has gone.
const DefaultTTL = 25 * time.Minute

func validate(o *Obligation) error {
	switch {
	case o == nil:
		return errors.New("purchase: nothing to propose")
	case strings.TrimSpace(o.MerchantHost) == "":
		return errors.New("purchase: I need the merchant's host before I can bind a purchase to it")
	case strings.TrimSpace(o.Currency) == "":
		return errors.New("purchase: I need the currency")
	case o.TotalCents <= 0:
		return errors.New("purchase: the total has to be a real amount")
	case len(o.Cart) == 0:
		return errors.New("purchase: I will not bind a purchase with an empty cart")
	}
	return nil
}

func (s *Store) byFingerprint(ctx context.Context, key string) (*Obligation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+obligationCols+`
		  FROM mem_purchase_obligations
		 WHERE idempotency_key = $1
		   AND status NOT IN ('cancelled','expired','failed')
		 LIMIT 1
	`, key)
	return scanObligation(row)
}

func (s *Store) Get(ctx context.Context, id string) (*Obligation, error) {
	if s == nil || s.pool == nil || strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `SELECT `+obligationCols+` FROM mem_purchase_obligations WHERE id = $1::uuid`, id)
	return scanObligation(row)
}

// setStatus is the single chokepoint for every status change. It enforces the
// transition table in SQL as well as in Go: the WHERE clause names the status
// the caller believed it was leaving, so a concurrent change loses rather than
// overwrites.
func (s *Store) setStatus(ctx context.Context, id, from, to string, extra string, args ...any) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("purchase: %s cannot become %s", from, to)
	}
	q := `UPDATE mem_purchase_obligations
	         SET status = $2, updated_at = NOW()` + extra + `
	       WHERE id = $1::uuid AND status = $3`
	all := append([]any{id, to, from}, args...)
	tag, err := s.pool.Exec(ctx, q, all...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("purchase: it was no longer %s when I tried to move it to %s", from, to)
	}
	return nil
}

// AwaitApproval moves a draft into the approval queue and records which Trust
// contract is carrying it.
func (s *Store) AwaitApproval(ctx context.Context, id, contractID string) error {
	return s.setStatus(ctx, id, StatusDraft, StatusPending,
		`, trust_contract_id = NULLIF($4,'')::uuid`, contractID)
}

// Approve records the boss's yes.
func (s *Store) Approve(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusPending, StatusApproved, ``)
}

// Claim is THE no-double-charge primitive.
//
// One atomic UPDATE. Exactly one caller can move an obligation out of
// 'approved', so a loop that re-executes the tool after approval, a
// TrustExecutor replay, a self-heal retry and a second operator all converge on
// the same outcome: the first wins, everyone else gets ErrNotClaimable and must
// stop. No lock, no coordination, no window.
func (s *Store) Claim(ctx context.Context, id string) (*Obligation, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("purchase: no database")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE mem_purchase_obligations
		   SET status = 'claimed', claimed_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid
		   AND status = 'approved'
		   AND expires_at > NOW()
	`, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotClaimable
	}
	return s.Get(ctx, id)
}

// MarkSubmitted is stamped BEFORE the charge-bearing click. See the package
// comment: this ordering is the difference between a crash reading as "may
// have charged" and reading as "never happened".
func (s *Store) MarkSubmitted(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusClaimed, StatusSubmitted, `, submitted_at = NOW()`)
}

// MarkAwaiting3DS parks a purchase that needs the boss to answer his bank.
func (s *Store) MarkAwaiting3DS(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusClaimed, StatusAwaiting3DS, ``)
}

// Resume3DS moves back to submitted once the boss has cleared the challenge.
func (s *Store) Resume3DS(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusAwaiting3DS, StatusSubmitted, `, submitted_at = COALESCE(submitted_at, NOW())`)
}

// Confirm records a VERIFIED purchase. Callers must have an order id: success
// is defined as having a receipt, not as having seen a page that looked right.
func (s *Store) Confirm(ctx context.Context, id string, receipt map[string]any) error {
	b, _ := json.Marshal(orEmpty(receipt))
	return s.setStatus(ctx, id, StatusSubmitted, StatusConfirmed,
		`, confirmed_at = NOW(), confirmation = $4::jsonb`, b)
}

// MarkUncertain is the honest outcome when we cannot prove what happened. It
// is terminal on purpose: resolving it is a human or a later reconciliation
// against the merchant, never an automatic retry.
func (s *Store) MarkUncertain(ctx context.Context, id, why string) error {
	b, _ := json.Marshal(map[string]any{"reason": why, "at": time.Now().UTC()})
	return s.setStatus(ctx, id, StatusSubmitted, StatusUncertain, `, failure = $4::jsonb`, b)
}

// Fail records a purchase that provably did not happen.
func (s *Store) Fail(ctx context.Context, id, from, why string) error {
	b, _ := json.Marshal(map[string]any{"reason": why, "at": time.Now().UTC()})
	return s.setStatus(ctx, id, from, StatusFailed, `, failure = $4::jsonb`, b)
}

// Cancel withdraws a purchase before anything was charged.
func (s *Store) Cancel(ctx context.Context, id, from, why string) error {
	b, _ := json.Marshal(map[string]any{"reason": why, "at": time.Now().UTC()})
	return s.setStatus(ctx, id, from, StatusCancelled, `, failure = $4::jsonb`, b)
}

// SweepStranded runs at boot. A row still 'submitted' after a restart is one
// where the process died between the click and the confirmation, so it becomes
// 'uncertain' and stops there. It is NEVER retried: the whole reason the write
// order is what it is would be wasted if the recovery re-ran the charge.
func (s *Store) SweepStranded(ctx context.Context, olderThan time.Duration) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE mem_purchase_obligations
		   SET status = 'uncertain',
		       updated_at = NOW(),
		       failure = jsonb_build_object(
		           'reason', 'I was interrupted after submitting this and never saw the confirmation, so I cannot tell you whether it went through. I have not retried it.',
		           'at', NOW())
		 WHERE status = 'submitted'
		   AND submitted_at < NOW() - $1::interval
	`, olderThan.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ExpireStale closes out obligations nobody acted on.
func (s *Store) ExpireStale(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE mem_purchase_obligations
		   SET status = 'expired', updated_at = NOW()
		 WHERE status IN ('draft','pending_approval','approved')
		   AND expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── scanning ──────────────────────────────────────────────────────────────

type scannable interface{ Scan(dest ...any) error }

func scanObligation(r scannable) (*Obligation, error) {
	var (
		o         Obligation
		cart      []byte
		recipient []byte
		confirm   []byte
		failure   []byte
	)
	err := r.Scan(&o.ID, &o.SessionID, &o.TrustContractID, &o.MerchantHost, &o.MerchantName,
		&cart, &o.Currency, &o.TotalCents, &recipient, &o.PaymentOrigins, &o.BrowserSession,
		&o.CardID, &o.Status, &o.IdempotencyKey, &o.ExpiresAt, &o.ClaimedAt, &o.SubmittedAt,
		&o.ConfirmedAt, &confirm, &failure, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal(cart, &o.Cart)
	_ = json.Unmarshal(recipient, &o.Recipient)
	_ = json.Unmarshal(confirm, &o.Confirmation)
	_ = json.Unmarshal(failure, &o.Failure)
	return &o, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
