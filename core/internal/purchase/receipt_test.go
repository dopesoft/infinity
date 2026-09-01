package purchase

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/surface"
)

// These guard the SAME failure mode, which is why they sit together: work that
// runs, produces something, and has its result quietly dropped because the one
// line that keeps it was never written. The confirmation screenshot did exactly
// that for the life of the feature — captured on every purchase, stored on none.
//
// WHAT ACTUALLY PREVENTS THE ORIGINAL BUG IS NOT A TEST HERE, AND SAYING SO
// MATTERS. A test asserting "the caller passed the screenshot" would be
// asserting on a line of code rather than on behaviour. The real guard is that
// Store.Confirm now REQUIRES the bytes as a parameter, so a caller cannot
// forget them the way the old one did: it would not compile. These tests cover
// what a signature cannot — that the decode is lossless, that a malformed image
// is dropped rather than stored as corrupt evidence, that the receipt reaches a
// surface he reads, and that failing to file one never turns a completed
// purchase into a failed one.

func TestConfirmationImageSurvivesTheRoundTrip(t *testing.T) {
	// A 1x1 PNG, the shape ScreenshotDirect returns.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	got := decodeDataURL(url)
	if len(got) == 0 {
		t.Fatal("the confirmation picture decoded to nothing, so it would be stored as nothing")
	}
	if string(got) != string(png) {
		t.Fatalf("the picture came back altered:\n want % x\n got  % x", png, got)
	}
}

func TestAMalformedImageIsDroppedRatherThanStoredCorrupt(t *testing.T) {
	// A receipt with no picture is fine. A receipt with a corrupt one is worse
	// than none, because it looks like evidence and is not.
	for _, bad := range []string{
		"",
		"not a data url",
		"data:image/png;base64,%%%not-base64%%%",
		"data:image/png;base64", // no comma
	} {
		if got := decodeDataURL(bad); got != nil {
			t.Fatalf("decodeDataURL(%q) returned %d bytes; malformed input must yield nil", bad, len(got))
		}
	}
}

// The receipt has to reach a surface the boss actually reads. 'system' folds
// into Activity rather than the inbox, which is a mistake this codebase has
// made before and written down (CLAUDE.md, surface key routing).
func TestReceiptDoesNotGoToASurfaceHeNeverReads(t *testing.T) {
	o := &Obligation{
		ID:           "11111111-1111-1111-1111-111111111111",
		MerchantName: "Allbirds",
		MerchantHost: "allbirds.com",
		Currency:     "USD",
		TotalCents:   9800,
		Cart:         []LineItem{{Title: "Wool Runners", Quantity: 1, UnitCents: 9800}},
	}
	rec := &recordingSurfacer{}
	tool := &executeTool{surface: rec}
	tool.surfaceReceipt(t.Context(), o, Receipt{OrderID: "A-123", TotalCents: 9800, Currency: "USD"})

	if rec.last == nil {
		t.Fatal("the purchase filed no receipt anywhere")
	}
	if rec.last.Surface == "system" {
		t.Fatal("the receipt went to 'system', which folds into Activity instead of the inbox he reads")
	}
	if rec.last.ExternalID != o.ID {
		t.Fatalf("the receipt is not keyed to its purchase (%q), so a re-run would duplicate it", rec.last.ExternalID)
	}
	if !strings.Contains(rec.last.Title, "Allbirds") {
		t.Fatalf("the receipt title does not say who he bought from: %q", rec.last.Title)
	}
	if !strings.Contains(rec.last.Body, "Wool Runners") {
		t.Fatalf("the receipt does not say what he bought: %q", rec.last.Body)
	}
}

// Surfacing is best effort BY DESIGN: the money has already moved, so a
// failure to file the receipt must never read as a failed purchase.
func TestAFailedReceiptDoesNotBecomeAFailedPurchase(t *testing.T) {
	tool := &executeTool{surface: &failingSurfacer{}}
	// The contract is that this returns nothing and panics on nothing. If it
	// ever grows an error return, the caller must still confirm the order.
	tool.surfaceReceipt(t.Context(), &Obligation{ID: "x", MerchantHost: "shop.com"}, Receipt{})

	// A nil surfacer is the no-vault case and must be equally harmless.
	(&executeTool{}).surfaceReceipt(t.Context(), &Obligation{ID: "x"}, Receipt{})
}

// The cart is what he checks a receipt against, so it has to read like the
// thing he bought rather than like a row.
func TestCartSummaryReadsLikeAReceipt(t *testing.T) {
	o := &Obligation{Cart: []LineItem{
		{Title: "Wool Runners", Quantity: 1},
		{Title: "Insoles", Quantity: 2},
		{Title: "   ", Quantity: 1}, // blank titles are skipped, not rendered empty
	}}
	got := o.CartSummary()
	if !strings.Contains(got, "Wool Runners") || !strings.Contains(got, "Insoles × 2") {
		t.Fatalf("cart summary lost something: %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("a blank line item rendered as an empty row: %q", got)
	}
	if (&Obligation{}).CartSummary() != "" {
		t.Fatal("an empty cart must summarise to nothing, not to a stray separator")
	}
}

// ── doubles ───────────────────────────────────────────────────────────────

type recordingSurfacer struct{ last *surface.Item }

func (r *recordingSurfacer) Upsert(_ context.Context, it *surface.Item) (string, error) {
	r.last = it
	return "id", nil
}

type failingSurfacer struct{}

func (failingSurfacer) Upsert(_ context.Context, _ *surface.Item) (string, error) {
	return "", errors.New("the surface store is down")
}
