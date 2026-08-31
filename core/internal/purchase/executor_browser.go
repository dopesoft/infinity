package purchase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/browser"
	"github.com/dopesoft/infinity/core/internal/vault"
)

// BrowserExecutor is the fill boundary: the only code that ever holds a card
// number, and the only code that clicks a button that spends money.
//
// WHAT MAKES IT A BOUNDARY
//
//   - It drives the browser through Registry's direct verbs, NOT through the
//     tool registry. So the card is never a tool input, never enters the
//     model's context, and never reaches an observation payload.
//   - It re-checks the obligation against the live page after claiming and
//     immediately before submitting, so an approval cannot be spent on terms
//     that changed while it waited.
//   - It claims the charge exactly once and never retries. A timeout, a
//     changed page or an unreadable outcome all resolve to uncertain, which
//     stops.
//   - It fills the security code LAST, so a form that submits early cannot
//     take a complete card with it.
type BrowserExecutor struct {
	reg *browser.Registry
	// details is the boss's own record: name, email, address. Nil-safe, so a
	// deployment without it fills the card and leaves the rest of the form to
	// whatever the merchant already knows.
	details *vault.Details
}

func NewBrowserExecutor(reg *browser.Registry, details *vault.Details) *BrowserExecutor {
	return &BrowserExecutor{reg: reg, details: details}
}

func (e *BrowserExecutor) Name() string { return "browser" }

// CanHandle requires a live session. There is no "open one and find your way
// back to the checkout": the cart lives in a specific browser, and a fresh
// browser is a different, unverified page.
func (e *BrowserExecutor) CanHandle(ctx context.Context, o *Obligation) bool {
	return e != nil && e.reg != nil && o != nil &&
		strings.TrimSpace(o.BrowserSession) != "" && e.reg.IsLive(o.BrowserSession)
}

// confirmSettle is how long we give a merchant to render its confirmation
// after the submit. Generous, because the alternative to waiting is declaring
// an outcome we have not seen.
const confirmSettle = 45 * time.Second

// Execute pays. The obligation must already be CLAIMED by the caller: claiming
// is what makes this single-shot, and doing it here would let two callers into
// this function before either had claimed.
func (e *BrowserExecutor) Execute(ctx context.Context, o *Obligation, card vault.Secrets) (Receipt, error) {
	var out Receipt
	if !e.CanHandle(ctx, o) {
		return out, errors.New("the browser session this purchase was agreed in is gone, so I stopped rather than start a new one on a page you have not seen")
	}
	if o.Status != StatusClaimed {
		return out, fmt.Errorf("purchase: refusing to pay an obligation in state %q", o.Status)
	}

	// RE-CHECK #2, against the live page, after the claim and before anything
	// is typed. This is the check the whole design exists for.
	page, err := e.page(ctx, o.BrowserSession)
	if err != nil {
		return out, fmt.Errorf("I could not read the checkout to re-check it before paying: %w", err)
	}
	if err := VerifyTerms(o, page); err != nil {
		return out, err
	}

	// Fill. Order is deliberate: number, then expiry, then the security code
	// last, so a form that submits on its own mid-fill cannot leave with a
	// complete card.
	fields, err := e.fields(ctx, o.BrowserSession)
	if err != nil {
		return out, err
	}
	if err := e.fill(ctx, o, fields, card); err != nil {
		return out, err
	}

	// Everything past here is the charge itself, and must not be abandoned
	// halfway by an unrelated cancellation: a steer, a dropped socket or the
	// end of a turn would otherwise cut between the click and the
	// confirmation, which is the one gap that produces an unresolvable
	// outcome. The deadline still bounds it.
	payCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), confirmSettle+30*time.Second)
	defer cancel()

	submit, ok := findSubmit(fields)
	if !ok {
		return out, errors.New("I could not find the button that completes this order, so I stopped with the card entered and nothing submitted")
	}

	// The caller stamps `submitted` BEFORE this returns, so if we die during
	// the click the row already says a charge may exist.
	if _, err := e.reg.ActDirect(payCtx, o.BrowserSession, browser.ActRequest{
		Index: submit, Action: "click",
	}); err != nil {
		// A click that errored may still have dispatched. This is exactly the
		// case that must never be retried.
		return out, fmt.Errorf("%w: the submit did not come back cleanly (%v)", ErrUncertain, err)
	}

	// Verify. Poll for a confirmation carrying a real order id.
	deadline := time.Now().Add(confirmSettle)
	for time.Now().Before(deadline) {
		st, err := e.page(payCtx, o.BrowserSession)
		if err == nil {
			if needs3DS(st) {
				return out, ErrNeeds3DS
			}
			if id := ConfirmationOf(st); id != "" {
				shot, _ := e.reg.ScreenshotDirect(payCtx, o.BrowserSession)
				out = Receipt{
					OrderID: id, URL: st.URL,
					TotalCents: o.TotalCents, Currency: o.Currency,
				}
				if shot != nil {
					out.Screenshot = shot.DataURL
				}
				return out, nil
			}
		}
		select {
		case <-payCtx.Done():
			return out, fmt.Errorf("%w: I ran out of time waiting for the confirmation", ErrUncertain)
		case <-time.After(2 * time.Second):
		}
	}
	// No order id. NOT a failure and NOT a success: we genuinely do not know,
	// and saying either would be a lie the boss would act on.
	return out, fmt.Errorf("%w: the order never showed a confirmation number", ErrUncertain)
}

// page reads the live page into the shape VerifyTerms understands.
func (e *BrowserExecutor) page(ctx context.Context, sessionID string) (PageState, error) {
	res, err := e.reg.ObserveDirect(ctx, sessionID)
	if err != nil {
		return PageState{}, err
	}
	st := PageState{URL: res.URL, Title: res.Title, Text: res.Text}
	seen := map[string]bool{}
	for _, el := range res.Elements {
		if el.FrameOrigin != "" && !seen[el.FrameOrigin] {
			seen[el.FrameOrigin] = true
			st.FrameOrigins = append(st.FrameOrigins, el.FrameOrigin)
		}
	}
	return st, nil
}

func (e *BrowserExecutor) fields(ctx context.Context, sessionID string) ([]browser.Element, error) {
	res, err := e.reg.ObserveDirect(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return res.Elements, nil
}

// fill types the card in, number → expiry → code.
func (e *BrowserExecutor) fill(ctx context.Context, o *Obligation, fields []browser.Element, card vault.Secrets) error {
	type step struct {
		kind  string
		value string
	}
	// His own details first, since a checkout normally asks who you are and
	// where it is going before it asks how you are paying. Only details he has
	// marked releasable come back from Release, and a withheld one is never
	// loaded rather than filtered later, so there is nothing here to leak.
	var steps []step
	if e.details != nil {
		release, err := e.details.Release(ctx)
		if err != nil {
			return fmt.Errorf("I could not read your details to fill this checkout: %w", err)
		}
		for _, spec := range vault.Catalog {
			if len(spec.Autofill) == 0 {
				continue
			}
			if v := release[spec.Key]; strings.TrimSpace(v) != "" {
				steps = append(steps, step{spec.Key, v})
			}
		}
	}
	// Security code LAST, always.
	steps = append(steps,
		step{"cc-number", card.PAN},
		step{"cc-exp", expiry(card)},
		step{"cc-name", card.Name},
		step{"cc-csc", card.CVC},
	)
	used := map[int]bool{}
	for _, s := range steps {
		if strings.TrimSpace(s.value) == "" {
			continue
		}
		idx, ok := findField(fields, s.kind, used)
		if !ok {
			if s.kind == "cc-number" || s.kind == "cc-csc" {
				return fmt.Errorf("I could not find the %s box on this checkout, so I stopped without entering anything", human(s.kind))
			}
			// Everything else is skipped rather than fatal: a checkout that
			// already knows his address, or never asks for a phone number, is
			// normal and not a failure.
			continue
		}
		used[idx] = true
		if _, err := e.reg.ActDirect(ctx, o.BrowserSession, browser.ActRequest{
			Index: idx, Action: "type", Value: s.value,
		}); err != nil {
			return fmt.Errorf("I could not enter the %s: %w", human(s.kind), err)
		}
	}
	return nil
}

func expiry(card vault.Secrets) string { return card.Billing["exp"] }

func human(kind string) string {
	switch kind {
	case "cc-number":
		return "card number"
	case "cc-exp":
		return "expiry date"
	case "cc-csc":
		return "security code"
	case "cc-name":
		return "name on card"
	}
	return kind
}

// fieldHints maps a field kind to the markers merchants actually use. This is
// DATA rather than a chain of per-merchant branches, so a new checkout is a new
// row here, never a new code path (Rule #1).
var fieldHints = map[string][]string{
	"cc-number": {"cc-number", "cardnumber", "card_number", "card-number", "creditcardnumber", "encryptedcardnumber", "number"},
	"cc-exp":    {"cc-exp", "expiry", "exp-date", "expdate", "expiration", "cc-exp-month", "encryptedexpirydate"},
	"cc-csc":    {"cc-csc", "cvc", "cvv", "csc", "securitycode", "security-code", "encryptedsecuritycode"},
	"cc-name":   {"cc-name", "ccname", "cardholder", "name-on-card", "nameoncard"},
}

// The rest of a checkout — name, email, phone, shipping and billing address —
// is filled from the boss's own details, and the markers come from the SAME
// catalog the settings screen renders (vault.Catalog). One list, so a field he
// can type is a field a checkout can fill, and adding "company name" later
// updates both at once rather than one and then a bug report.
func init() {
	for _, spec := range vault.Catalog {
		if len(spec.Autofill) > 0 {
			fieldHints[spec.Key] = spec.Autofill
		}
	}
}

// findField picks the input for a card field by its own attributes.
//
// Matching on autocomplete/name/id/placeholder rather than position is what
// keeps this stable across redesigns, and matching a KIND rather than a
// merchant is what keeps it generic.
// disqualifiers stop a loose hint from claiming the wrong box.
//
// This is not hypothetical. "number" is one of the markers for a card number,
// and an input named `phoneNumber` contains it. Before the boss's phone number
// and address were on the same form that collision was rare; now these fields
// sit on the same checkout it would be routine, and the failure it produces is
// the worst kind — a card number typed into a box that is not a card box, on a
// page that then submits.
//
// So a candidate is rejected outright when its markers say it is something
// else, whatever the hint matched.
var disqualifiers = map[string][]string{
	"cc-number": {"phone", "tel", "account", "order", "tracking", "zip", "postal", "house", "street", "apt", "suite"},
	"cc-csc":    {"zip", "postal", "phone", "tel"},
	"cc-exp":    {"phone", "tel"},
	"cc-name":   {"user", "login", "company", "street", "city"},
	"phone":     {"card", "cc-", "account"},
	"email":     {"confirm"},
}

func disqualified(kind, hay string) bool {
	for _, bad := range disqualifiers[kind] {
		if strings.Contains(hay, bad) {
			return true
		}
	}
	return false
}

// findField picks the input for a field by its own attributes.
//
// `used` carries the indexes already filled this pass, so one input can never
// receive two different values. Without it a loose match could put the card
// number into a box that already holds the phone number, and the second write
// silently wins.
func findField(fields []browser.Element, kind string, used map[int]bool) (int, bool) {
	hints := fieldHints[kind]
	best, found := 0, false
	for _, el := range fields {
		if used[el.Idx] {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{
			el.Autocomplete, el.Name, el.Placeholder, el.Text,
		}, " "))
		if strings.TrimSpace(hay) == "" {
			continue
		}
		// An exact autocomplete match is the merchant telling us outright, and
		// it beats everything including a disqualifier, because at that point
		// the field has named itself.
		if strings.EqualFold(strings.TrimSpace(el.Autocomplete), kind) {
			return el.Idx, true
		}
		if disqualified(kind, hay) {
			continue
		}
		for _, h := range hints {
			if strings.Contains(hay, h) && !found {
				best, found = el.Idx, true
			}
		}
	}
	return best, found
}

// submitMarkers are the labels that complete an order. Matching here is safe in
// a way it never was on browser_act, because by this point the obligation has
// already bound the merchant, the cart and the total: this only decides WHICH
// button, not WHETHER to spend.
var submitMarkers = []string{
	"place order", "place your order", "complete order", "confirm order",
	"complete purchase", "pay now", "confirm and pay", "agree and pay",
	"submit payment", "buy now", "complete checkout", "place bid",
}

func findSubmit(fields []browser.Element) (int, bool) {
	for _, el := range fields {
		hay := strings.ToLower(strings.TrimSpace(el.Text + " " + el.Name))
		for _, m := range submitMarkers {
			if strings.Contains(hay, m) {
				return el.Idx, true
			}
		}
	}
	return 0, false
}

// needs3DS spots a bank challenge. Handing this to the boss is the correct
// outcome: he is the only one who can answer it, and guessing at it would fail
// the payment in a way that looks like our bug.
func needs3DS(st PageState) bool {
	hay := strings.ToLower(st.Title + " " + st.Text)
	for _, m := range []string{
		"3d secure", "3-d secure", "verified by visa", "securecode",
		"one-time passcode", "one time passcode", "authentication code sent",
		"verify your identity with your bank", "approve this payment in your banking app",
	} {
		if strings.Contains(hay, m) {
			return true
		}
	}
	return false
}
