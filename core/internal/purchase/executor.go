package purchase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dopesoft/infinity/core/internal/vault"
)

// Executor pays for an obligation. It is an interface so the RAIL can change
// without the callers changing.
//
// There are three rails in the world and they differ enormously in
// reliability. In descending order:
//
//  1. A protocol rail. The Agentic Commerce Protocol (Stripe + OpenAI, spec
//     2026-04-17) hands the merchant a scoped payment token and gets back a
//     real order with webhooks. No form filling, no vision, deterministic
//     success or failure.
//  2. A merchant API, where we have one.
//  3. A browser, typing into someone else's checkout page.
//
// Only the browser rail is implemented, because it is the only one a merchant
// we can actually test against supports today. Shipping an ACP executor with no
// live path would be built-but-not-wired, which the project treats as worse
// than absent. The interface exists so adding one later is an implementation,
// not a rewrite.
type Executor interface {
	Name() string
	CanHandle(ctx context.Context, o *Obligation) bool
	Execute(ctx context.Context, o *Obligation, card vault.Secrets) (Receipt, error)
}

// Receipt is proof, and an order id is the only part of it that counts.
//
// Success is defined as HAVING a receipt, not as having seen a page that looked
// like success. That distinction is what makes an uncertain outcome resolvable
// later against the merchant's own order history or the confirmation email,
// without anyone having to risk a second charge to find out.
type Receipt struct {
	OrderID    string         `json:"order_id"`
	URL        string         `json:"url,omitempty"`
	TotalCents int64          `json:"total_cents"`
	Currency   string         `json:"currency"`
	Screenshot string         `json:"screenshot,omitempty"` // masked, stored separately
	Extra      map[string]any `json:"extra,omitempty"`
}

// ErrNeeds3DS parks a purchase for the boss rather than failing it. A bank
// challenge is not an error: it is the one step only he can complete.
var ErrNeeds3DS = errors.New("this checkout wants a verification step only you can complete")

// ErrUncertain means we cannot prove what happened. Callers must resolve the
// obligation to 'uncertain' and STOP. Never retry on this.
var ErrUncertain = errors.New("I could not confirm whether that went through")

// PageState is what the fill boundary can see about the live page. Keeping the
// executor behind this rather than a browser client means the re-check logic is
// testable without a browser.
type PageState struct {
	URL          string
	Title        string
	Text         string
	FrameOrigins []string
}

// OriginAllowed reports whether a URL's origin is one the obligation permits to
// receive card data.
//
// This is what stops a card being typed into a page that merely resembles the
// checkout: a redirect to a lookalike host, or a third-party frame that drifted
// in. The obligation names the origins at proposal time and they are not
// widened later by anything the model says.
func OriginAllowed(o *Obligation, raw string) bool {
	host := hostOf(raw)
	if host == "" {
		return false
	}
	if sameSite(host, o.MerchantHost) {
		return true
	}
	for _, allowed := range o.PaymentOrigins {
		if a := hostOf(allowed); a != "" && sameSite(host, a) {
			return true
		}
	}
	return false
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// sameSite compares registrable-ish suffixes so www.merchant.com and
// checkout.merchant.com match, while merchant.com.evil.example does not.
func sameSite(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

// VerifyTerms re-checks the live page against what the boss approved.
//
// It runs TWICE: once before claiming, and again after claiming and
// immediately before submitting. The second run is the one that matters and is
// the reason the whole obligation exists. A cart can change during an approval
// wait: a price moves, an item goes out of stock, shipping recalculates. The
// boss agreed to an amount, so a different amount is a different purchase, and
// the only correct response is to stop and re-propose rather than to charge
// something he never saw.
func VerifyTerms(o *Obligation, page PageState) error {
	if !OriginAllowed(o, page.URL) {
		return fmt.Errorf("the checkout is now on %s, which is not where this purchase was agreed (%s), so I stopped",
			hostOf(page.URL), o.MerchantHost)
	}
	if o.TotalCents <= 0 {
		return errors.New("this purchase has no agreed total, so there is nothing for me to check against")
	}
	if !mentionsTotal(page.Text, o.TotalCents) {
		return fmt.Errorf("I could not see the agreed total of %s anywhere on the checkout, so I stopped rather than pay an amount you have not seen", o.Total())
	}
	for _, origin := range page.FrameOrigins {
		if !OriginAllowed(o, origin) {
			return fmt.Errorf("part of this checkout is being served by %s, which this purchase does not authorise to take a card, so I stopped",
				hostOf(origin))
		}
	}
	return nil
}

// mentionsTotal looks for the agreed amount on the page in the forms a
// merchant actually renders it.
//
// Deliberately a presence check rather than a parse. Parsing "the total" out of
// arbitrary checkout markup is exactly the kind of brittle guess that fails
// silently, and a false confident parse would be worse than no check at all.
// Absence is treated as a stop, so the failure mode is refusing to pay, never
// paying the wrong number.
func mentionsTotal(text string, cents int64) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	whole := cents / 100
	frac := cents % 100
	needles := []string{
		fmt.Sprintf("%d.%02d", whole, frac),
		fmt.Sprintf("%s.%02d", withThousands(whole), frac),
	}
	if frac == 0 {
		needles = append(needles, fmt.Sprintf("%d", whole), withThousands(whole))
	}
	hay := strings.ReplaceAll(text, ",", "")
	for _, n := range needles {
		if strings.Contains(text, n) || strings.Contains(hay, strings.ReplaceAll(n, ",", "")) {
			return true
		}
	}
	return false
}

func withThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// ConfirmationOf extracts a merchant order id from a confirmation page.
//
// Returning "" means NOT CONFIRMED, and the caller must resolve the obligation
// to uncertain. That is the honest outcome: a page that says "thank you" with
// no order number is not evidence a charge succeeded, and treating it as one is
// how a system starts reporting purchases that never happened.
func ConfirmationOf(page PageState) string {
	text := page.Text
	markers := []string{
		"order number", "order #", "order id", "confirmation number",
		"order confirmation", "receipt number",
	}
	lower := strings.ToLower(text)
	for _, m := range markers {
		i := strings.Index(lower, m)
		if i < 0 {
			continue
		}
		if id := firstToken(text[i+len(m):]); id != "" {
			return id
		}
	}
	return ""
}

// firstToken pulls the first plausible id out of the text after a marker: a run
// of digits, letters and dashes, at least four characters so a stray "1" from
// "1 item" cannot pass as an order number.
func firstToken(s string) string {
	var b strings.Builder
	started := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '-':
			b.WriteRune(r)
			started = true
		default:
			if started {
				out := b.String()
				if len(out) >= 4 && strings.ContainsAny(out, "0123456789") {
					return out
				}
				b.Reset()
				started = false
			}
		}
		if b.Len() > 40 {
			break
		}
	}
	out := b.String()
	if len(out) >= 4 && strings.ContainsAny(out, "0123456789") {
		return out
	}
	return ""
}
