package purchase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/browser"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/vault"
)

// The agent's whole vocabulary for spending money.
//
// Note what is NOT here. There is no tool that takes a card number, and no
// tool that returns one. purchase_execute takes an obligation id and nothing
// else: it cannot be talked into a different merchant, a different total or a
// different card, because those were fixed when the boss approved. The card is
// spliced in server-side inside Execute, exactly as the phone tool has always
// done with call briefs.

// Register wires the purchase and wallet tools.
func Register(r *tools.Registry, store *Store, cards vault.CardVault, reg *browser.Registry, publicURL string) {
	if r == nil || store == nil {
		return
	}
	r.Register(&proposeTool{store: store, reg: reg, cards: cards})
	r.Register(&executeTool{store: store, cards: cards, exec: NewBrowserExecutor(reg)})
	r.Register(&statusTool{store: store})
	if cards != nil {
		r.Register(&cardListTool{cards: cards})
		if vs, ok := cards.(*vault.Store); ok {
			r.Register(&enrollTool{vault: vs, publicURL: publicURL})
		}
	}
}

// ── purchase_propose ──────────────────────────────────────────────────────

type proposeTool struct {
	store *Store
	reg   *browser.Registry
	cards vault.CardVault
}

func (t *proposeTool) Name() string { return "purchase_propose" }
func (t *proposeTool) Description() string {
	return "Bind a purchase you are about to make so the boss can approve the exact terms. " +
		"Read the merchant, the line items and the TOTAL off the checkout page you are on, and pass them here. " +
		"This does not spend anything: it records what would be spent. Follow with purchase_execute, which stops for his approval. " +
		"If the cart changes afterwards the approval no longer applies and you must propose again."
}
func (t *proposeTool) ReadOnly() bool { return true }

func (t *proposeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"merchant_host": map[string]any{"type": "string", "description": "Host of the checkout, e.g. www.bestbuy.com"},
			"merchant_name": map[string]any{"type": "string", "description": "How the boss would name this merchant"},
			"currency":      map[string]any{"type": "string", "description": "ISO code, e.g. USD"},
			"total_cents":   map[string]any{"type": "integer", "description": "The EXACT total shown on the checkout, in minor units. 4999 for $49.99."},
			"cart": map[string]any{
				"type":        "array",
				"description": "Line items exactly as the checkout lists them",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":      map[string]any{"type": "string"},
						"quantity":   map[string]any{"type": "integer"},
						"unit_cents": map[string]any{"type": "integer"},
						"url":        map[string]any{"type": "string"},
					},
					"required": []string{"title"},
				},
			},
			"payment_origins": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Extra origins allowed to take the card, e.g. js.stripe.com when the card fields are in a Stripe frame. The merchant's own host is always allowed.",
			},
			"card_id":   map[string]any{"type": "string", "description": "Which stored card to use (from vault_card_list). Omit when the merchant already has a card on file."},
			"recipient": map[string]any{"type": "object", "description": "Where it ships, when relevant"},
		},
		"required": []string{"merchant_host", "currency", "total_cents", "cart"},
	}
}

func (t *proposeTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	o := &Obligation{
		SessionID:      tools.SessionIDFromContext(ctx),
		MerchantHost:   str(in, "merchant_host"),
		MerchantName:   str(in, "merchant_name"),
		Currency:       str(in, "currency"),
		TotalCents:     int64(num(in, "total_cents")),
		CardID:         str(in, "card_id"),
		PaymentOrigins: strList(in, "payment_origins"),
		Cart:           cartOf(in),
	}
	if m, ok := in["recipient"].(map[string]any); ok {
		o.Recipient = m
	}
	// Bind the browser this cart actually lives in. An approval must not be
	// spendable in a different browser on a different page.
	if t.reg != nil {
		if id, ok := t.reg.Resolve(o.SessionID, ""); ok {
			o.BrowserSession = id
		}
	}
	if o.CardID != "" && t.cards != nil {
		if _, err := t.cards.Get(ctx, o.CardID); err != nil {
			return "", fmt.Errorf("I could not find that card in the wallet: %w", err)
		}
	}

	saved, created, err := t.store.Propose(ctx, o)
	if err != nil {
		return "", err
	}
	if !created {
		return fmt.Sprintf("This is the same purchase I already had open (%s), so I did not create a second one.\n%s",
			saved.ID, describe(saved)), nil
	}
	return fmt.Sprintf("Bound this purchase as %s. Nothing has been spent.\n%s\n\nCall purchase_execute with this id when you are ready; it will stop for the boss's approval.",
		saved.ID, describe(saved)), nil
}

// ── purchase_execute ──────────────────────────────────────────────────────

type executeTool struct {
	store *Store
	cards vault.CardVault
	exec  Executor
}

func (t *executeTool) Name() string { return "purchase_execute" }
func (t *executeTool) Description() string {
	return "Pay for a purchase you already bound with purchase_propose. Takes the obligation id and nothing else. " +
		"This stops for the boss's approval, then pays exactly the total he approved and returns the order number. " +
		"It runs at most once per purchase: if it says the purchase was already claimed, do NOT call it again."
}

func (t *executeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"obligation_id": map[string]any{"type": "string", "description": "The id purchase_propose returned"},
		},
		"required": []string{"obligation_id"},
	}
}

func (t *executeTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := str(in, "obligation_id")
	o, err := t.store.Get(ctx, id)
	if err != nil {
		return "", err
	}

	// A purchase only runs in the session that bound it. TrustExecutor's
	// deferred path has no session, and a replay hours later would re-check a
	// cart against a browser that no longer exists.
	if tools.SessionIDFromContext(ctx) == "" {
		return "", errors.New("a purchase only runs in the conversation it was agreed in, and this call has no session, so I stopped")
	}

	// Claim. Exactly one caller can win this, which is what makes the loop's
	// re-execution after approval, a self-heal retry and a deferred replay all
	// converge on one charge.
	claimed, err := t.store.Claim(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotClaimable) {
			// Deliberately NOT an error: a non-nil error sets
			// toolErredThisTurn, which arms self-heal to retry, and retrying a
			// purchase is the one thing that must never happen.
			return fmt.Sprintf("That purchase (%s) was already started, so I did not run it again. Its state is %q. "+
				"Use purchase_status to see how it ended.", id, o.Status), nil
		}
		return "", err
	}

	var secrets vault.Secrets
	if claimed.CardID != "" {
		if t.cards == nil || !t.cards.Healthy() {
			_ = t.store.Cancel(ctx, id, StatusClaimed, "vault unavailable")
			return "", errors.New("the card vault is not available, so I stopped rather than try to pay another way. Set INFINITY_VAULT_KEY on core")
		}
		// The ONLY place a card is decrypted. It never returns to the model.
		secrets, err = t.cards.Open(ctx, claimed.CardID)
		if err != nil {
			_ = t.store.Cancel(ctx, id, StatusClaimed, "card unreadable")
			return "", fmt.Errorf("I could not read that card, so I stopped: %w", err)
		}
	}

	// Stamp submitted BEFORE handing off, so a crash inside Execute reads as
	// "may have charged" rather than "never happened".
	if err := t.store.MarkSubmitted(ctx, id); err != nil {
		return "", err
	}

	receipt, err := t.exec.Execute(ctx, claimed, secrets)
	switch {
	case errors.Is(err, ErrNeeds3DS):
		return "The bank wants a verification step only the boss can complete. I have left the checkout open on that screen and stopped. " +
			"Ask him to take over the browser, clear it, and tell you when it is done.", nil
	case errors.Is(err, ErrUncertain):
		_ = t.store.MarkUncertain(ctx, id, err.Error())
		return "", fmt.Errorf("I submitted that order and then could not confirm it went through, so I have NOT tried again. "+
			"Check the merchant's order history or the confirmation email before anyone retries: %w", err)
	case err != nil:
		_ = t.store.Fail(ctx, id, StatusSubmitted, err.Error())
		return "", err
	}

	if err := t.store.Confirm(ctx, id, map[string]any{
		"order_id": receipt.OrderID, "url": receipt.URL,
		"total_cents": receipt.TotalCents, "currency": receipt.Currency,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Done. %s at %s, order number %s.",
		claimed.Total(), hostLabel(claimed), receipt.OrderID), nil
}

// ── purchase_status ───────────────────────────────────────────────────────

type statusTool struct{ store *Store }

func (t *statusTool) Name() string { return "purchase_status" }
func (t *statusTool) Description() string {
	return "Look up where a purchase got to, including its order number if it completed."
}
func (t *statusTool) ReadOnly() bool { return true }
func (t *statusTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"obligation_id": map[string]any{"type": "string"}},
		"required":   []string{"obligation_id"},
	}
}

func (t *statusTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	o, err := t.store.Get(ctx, str(in, "obligation_id"))
	if err != nil {
		return "", err
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s\nState: %s\n", describe(o), o.Status)
	if id, ok := o.Confirmation["order_id"].(string); ok && id != "" {
		fmt.Fprintf(b, "Order number: %s\n", id)
	}
	if why, ok := o.Failure["reason"].(string); ok && why != "" {
		fmt.Fprintf(b, "What went wrong: %s\n", why)
	}
	return b.String(), nil
}

// ── wallet ────────────────────────────────────────────────────────────────

type cardListTool struct{ cards vault.CardVault }

func (t *cardListTool) Name() string { return "vault_card_list" }
func (t *cardListTool) Description() string {
	return "List the boss's stored cards. You get a label, brand and last four only, never a number, and that is all you need to pick one."
}
func (t *cardListTool) ReadOnly() bool { return true }
func (t *cardListTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *cardListTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	if !t.cards.Healthy() {
		return "", errors.New("the card vault is not available (INFINITY_VAULT_KEY is not set on core), so I cannot see whether he has a card stored")
	}
	cards, err := t.cards.List(ctx)
	if err != nil {
		return "", err
	}
	if len(cards) == 0 {
		return "No cards stored yet. Use vault_enroll_link to give the boss a private link to add one; never ask him for the number in chat.", nil
	}
	var b strings.Builder
	for _, c := range cards {
		fmt.Fprintf(&b, "%s — %s ending %s (id %s)\n", c.Label, c.Brand, c.Last4, c.ID)
	}
	return b.String(), nil
}

type enrollTool struct {
	vault     *vault.Store
	publicURL string
}

func (t *enrollTool) Name() string { return "vault_enroll_link" }
func (t *enrollTool) Description() string {
	return "Create a private, single-use, ten-minute link the boss can use to add a card. " +
		"Use this INSTEAD of ever asking for a card number in chat: a number in a message is a number in the transcript."
}
func (t *enrollTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *enrollTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	token, expires, err := t.vault.NewEnrollment(ctx)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(t.publicURL, "/")
	if base == "" {
		base = "https://infinity.dopesoft.io"
	}
	return fmt.Sprintf("Here is a private link to add a card. It works once and expires at %s:\n%s/wallet/add?t=%s",
		expires.Format("15:04"), base, token), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func describe(o *Obligation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s", o.Total(), hostLabel(o))
	for _, li := range o.Cart {
		fmt.Fprintf(&b, "\n  - %s", li.Title)
		if li.Quantity > 1 {
			fmt.Fprintf(&b, " x%d", li.Quantity)
		}
	}
	return b.String()
}

func hostLabel(o *Obligation) string {
	if strings.TrimSpace(o.MerchantName) != "" {
		return o.MerchantName
	}
	return o.MerchantHost
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return strings.TrimSpace(s)
}

func num(in map[string]any, k string) float64 {
	switch v := in[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func strList(in map[string]any, k string) []string {
	raw, _ := in[k].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func cartOf(in map[string]any) []LineItem {
	raw, _ := in["cart"].([]any)
	out := make([]LineItem, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		li := LineItem{
			Title:     str(m, "title"),
			Quantity:  int(num(m, "quantity")),
			UnitCents: int64(num(m, "unit_cents")),
			URL:       str(m, "url"),
			VariantID: str(m, "variant_id"),
		}
		if li.Quantity <= 0 {
			li.Quantity = 1
		}
		out = append(out, li)
	}
	return out
}
