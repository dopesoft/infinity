package purchase

import (
	"strings"
	"testing"
)

// These tests encode WHY the design is shaped the way it is, not just what it
// currently does. Each one corresponds to a way a purchase system charges
// somebody twice or charges them the wrong amount.

// ── the state machine ─────────────────────────────────────────────────────

func TestNothingEverReturnsToApproved(t *testing.T) {
	// The single most important absent edge. If any state could go back to
	// approved, it could be claimed a second time, and a second claim is a
	// second charge.
	for from := range allowed {
		if from == StatusPending {
			continue // pending -> approved is the boss saying yes, once
		}
		if CanTransition(from, StatusApproved) {
			t.Fatalf("%s can return to approved, which makes a second claim possible", from)
		}
	}
}

func TestSubmittedOnlyEverResolves(t *testing.T) {
	// Once we have clicked, the only honest moves are: it worked, we cannot
	// tell, or it definitely failed. Anything else would re-open a charge that
	// may already exist.
	want := map[string]bool{StatusConfirmed: true, StatusUncertain: true, StatusFailed: true}
	for _, to := range allowed[StatusSubmitted] {
		if !want[to] {
			t.Fatalf("submitted may become %q; a submitted charge must only ever resolve", to)
		}
	}
	if CanTransition(StatusSubmitted, StatusClaimed) {
		t.Fatal("submitted can be re-claimed, which is a second charge")
	}
}

func TestUncertainIsTerminal(t *testing.T) {
	// Uncertain is the outcome that protects the boss from a double charge.
	// Making it recoverable in code would hand that decision back to an
	// automatic retry, which is exactly what must not happen.
	if len(allowed[StatusUncertain]) != 0 {
		t.Fatalf("uncertain leads to %v; it must be terminal so nothing retries a charge we could not confirm", allowed[StatusUncertain])
	}
	for _, terminal := range []string{StatusConfirmed, StatusFailed, StatusCancelled, StatusExpired} {
		if len(allowed[terminal]) != 0 {
			t.Fatalf("%s is not terminal: %v", terminal, allowed[terminal])
		}
	}
}

func TestClaimOnlyFromApproved(t *testing.T) {
	for from := range allowed {
		if from == StatusApproved {
			continue
		}
		if CanTransition(from, StatusClaimed) {
			t.Fatalf("%s can be claimed; only an approved purchase may be", from)
		}
	}
}

// ── the fingerprint ───────────────────────────────────────────────────────

func TestSameCartIsAlwaysTheSameObligation(t *testing.T) {
	cart := []LineItem{{Title: "USB-C cable", Quantity: 2, UnitCents: 1299}}
	a := Fingerprint("www.bestbuy.com", "USD", 2598, cart)
	b := Fingerprint("www.bestbuy.com", "USD", 2598, cart)
	if a != b {
		t.Fatal("the same cart produced two fingerprints, so a second propose would create a rival obligation and the boss could approve the same purchase twice")
	}
}

func TestCartOrderIsNotPartOfIdentity(t *testing.T) {
	one := []LineItem{{Title: "cable", Quantity: 1, UnitCents: 100}, {Title: "case", Quantity: 1, UnitCents: 200}}
	two := []LineItem{{Title: "case", Quantity: 1, UnitCents: 200}, {Title: "cable", Quantity: 1, UnitCents: 100}}
	if Fingerprint("m.com", "USD", 300, one) != Fingerprint("m.com", "USD", 300, two) {
		t.Fatal("re-ordering the same cart changed its identity; the model listing items differently must not mint a second obligation")
	}
}

func TestADifferentTotalIsADifferentPurchase(t *testing.T) {
	cart := []LineItem{{Title: "cable", Quantity: 1, UnitCents: 1299}}
	if Fingerprint("m.com", "USD", 1299, cart) == Fingerprint("m.com", "USD", 12990, cart) {
		t.Fatal("a $12.99 cart and a $129.90 cart share a fingerprint; the amount is the thing being approved")
	}
}

func TestADifferentMerchantIsADifferentPurchase(t *testing.T) {
	cart := []LineItem{{Title: "cable", Quantity: 1, UnitCents: 1299}}
	if Fingerprint("bestbuy.com", "USD", 1299, cart) == Fingerprint("evil.example", "USD", 1299, cart) {
		t.Fatal("two merchants share a fingerprint")
	}
}

// ── origin binding ────────────────────────────────────────────────────────

func TestOriginBinding(t *testing.T) {
	o := &Obligation{
		MerchantHost:   "bestbuy.com",
		PaymentOrigins: []string{"js.stripe.com"},
	}
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"the merchant itself", "https://bestbuy.com/checkout", true},
		{"a subdomain of the merchant", "https://checkout.bestbuy.com/pay", true},
		{"the declared payment frame", "https://js.stripe.com/v3/elements", true},
		// The attack this exists to stop: a host that merely CONTAINS the
		// merchant's name.
		{"a lookalike suffix", "https://bestbuy.com.evil.example/checkout", false},
		{"an unrelated host", "https://evil.example/checkout", false},
		{"an undeclared payment processor", "https://checkout.adyen.com/pay", false},
		{"nothing at all", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OriginAllowed(o, c.url); got != c.want {
				t.Fatalf("OriginAllowed(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

// ── the re-check ──────────────────────────────────────────────────────────

func TestVerifyRefusesWhenTheTotalIsNotOnThePage(t *testing.T) {
	// The cart moved while the boss was deciding. He agreed to $25.98 and the
	// page now says something else, so the agreement does not cover this.
	o := &Obligation{MerchantHost: "shop.example", Currency: "USD", TotalCents: 2598}
	err := VerifyTerms(o, PageState{
		URL:  "https://shop.example/checkout",
		Text: "Order total $41.02",
	})
	if err == nil {
		t.Fatal("paid an amount that was not on the page; a changed cart must stop the purchase")
	}
	if !strings.Contains(err.Error(), "25.98") {
		t.Fatalf("the refusal does not name the amount he agreed to: %v", err)
	}
}

func TestVerifyAcceptsTheAgreedTotal(t *testing.T) {
	o := &Obligation{MerchantHost: "shop.example", Currency: "USD", TotalCents: 2598}
	for _, rendering := range []string{
		"Order total $25.98",
		"Total: USD 25.98 including tax",
		"25.98",
	} {
		if err := VerifyTerms(o, PageState{URL: "https://shop.example/checkout", Text: rendering}); err != nil {
			t.Fatalf("refused a page rendering the agreed total as %q: %v", rendering, err)
		}
	}
}

func TestVerifyRefusesAnUndeclaredPaymentFrame(t *testing.T) {
	o := &Obligation{MerchantHost: "shop.example", Currency: "USD", TotalCents: 1000}
	err := VerifyTerms(o, PageState{
		URL:          "https://shop.example/checkout",
		Text:         "Total $10.00",
		FrameOrigins: []string{"https://unknown-processor.example"},
	})
	if err == nil {
		t.Fatal("allowed a card to be typed into a frame the purchase never authorised")
	}
}

func TestVerifyRefusesAnEmptyPage(t *testing.T) {
	// An unreadable page is not a page that agrees with us. Failing open here
	// would pay on no evidence at all.
	o := &Obligation{MerchantHost: "shop.example", Currency: "USD", TotalCents: 1000}
	if err := VerifyTerms(o, PageState{URL: "https://shop.example/checkout", Text: ""}); err == nil {
		t.Fatal("an empty page passed verification")
	}
}

// ── confirmation ──────────────────────────────────────────────────────────

func TestConfirmationNeedsARealOrderNumber(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"order number", "Thank you! Order number 114-2938471-2938", "114-2938471-2938"},
		{"order hash", "Your order #A1B2C3D4 is confirmed", "A1B2C3D4"},
		{"confirmation number", "Confirmation number: XYZ-99182", "XYZ-99182"},

		// The important half. A page that merely LOOKS successful is not
		// evidence of a charge, and treating it as one is how a system starts
		// reporting purchases that never happened.
		{"thanks with no number", "Thank you for your order!", ""},
		{"empty", "", ""},
		{"a stray digit", "Order number 1 item", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ConfirmationOf(PageState{Text: c.text}); got != c.want {
				t.Fatalf("ConfirmationOf(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

// ── presentation ──────────────────────────────────────────────────────────

func TestTotalReadsLikeMoney(t *testing.T) {
	// The approval card is the boss's only defence against approving the wrong
	// thing, so the amount has to read as an amount.
	o := &Obligation{Currency: "usd", TotalCents: 2598}
	if got := o.Total(); got != "USD 25.98" {
		t.Fatalf("Total() = %q, want %q", got, "USD 25.98")
	}
	big := &Obligation{Currency: "USD", TotalCents: 129900}
	if got := big.Total(); got != "USD 1299.00" {
		t.Fatalf("Total() = %q", got)
	}
}

func TestValidateRefusesAnUnboundedPurchase(t *testing.T) {
	cases := []struct {
		name string
		o    *Obligation
	}{
		{"no merchant", &Obligation{Currency: "USD", TotalCents: 100, Cart: []LineItem{{Title: "x"}}}},
		{"no currency", &Obligation{MerchantHost: "m.com", TotalCents: 100, Cart: []LineItem{{Title: "x"}}}},
		{"no total", &Obligation{MerchantHost: "m.com", Currency: "USD", Cart: []LineItem{{Title: "x"}}}},
		{"negative total", &Obligation{MerchantHost: "m.com", Currency: "USD", TotalCents: -1, Cart: []LineItem{{Title: "x"}}}},
		{"empty cart", &Obligation{MerchantHost: "m.com", Currency: "USD", TotalCents: 100}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validate(c.o); err == nil {
				t.Fatal("accepted a purchase with nothing to bind it to")
			}
		})
	}
}
