package purchase

import (
	"testing"

	"github.com/dopesoft/infinity/core/internal/browser"
	"github.com/dopesoft/infinity/core/internal/vault"
)

// A checkout now carries the boss's name, phone and address alongside the card,
// and the matcher works on substrings. That combination has one genuinely
// dangerous failure: an input named `phoneNumber` contains "number", which is a
// marker for the CARD number. Getting that wrong types a live card into a box
// that is not a card box, on a page that is about to be submitted.
//
// These tests exist for that, and for the quieter version of it: two fields
// resolving to the same input, where the second write silently overwrites the
// first and the form submits with a card number where a name should be.

func el(idx int, autocomplete, name, placeholder string) browser.Element {
	return browser.Element{Idx: idx, Autocomplete: autocomplete, Name: name, Placeholder: placeholder}
}

func TestCardNumberNeverLandsInAPhoneBox(t *testing.T) {
	fields := []browser.Element{
		el(1, "", "phoneNumber", "Phone number"),
		el(2, "", "accountNumber", "Account number"),
		el(3, "", "cardNumber", "Card number"),
	}
	idx, ok := findField(fields, "cc-number", map[int]bool{})
	if !ok {
		t.Fatal("the card number box was not found at all")
	}
	if idx != 3 {
		t.Fatalf("the card number would have been typed into field %d, not the card box (3)", idx)
	}
}

func TestPhoneNeverLandsInACardBox(t *testing.T) {
	fields := []browser.Element{
		el(1, "", "cardNumber", "Card number"),
		el(2, "", "phone", "Phone"),
	}
	idx, ok := findField(fields, "phone", map[int]bool{})
	if !ok || idx != 2 {
		t.Fatalf("the phone number resolved to field %d (found=%v), not the phone box (2)", idx, ok)
	}
}

// An explicit autocomplete is the merchant naming the field itself, and it must
// win even when the surrounding text looks like something else.
func TestExplicitAutocompleteWins(t *testing.T) {
	fields := []browser.Element{
		el(1, "", "number", "Number"),
		el(2, "cc-number", "x-9f2a", "Enter your details"),
	}
	idx, ok := findField(fields, "cc-number", map[int]bool{})
	if !ok || idx != 2 {
		t.Fatalf("an exact autocomplete match lost to a guess: got %d (found=%v), want 2", idx, ok)
	}
}

// One input, one value. Without this a loose match can claim a box that has
// already been filled, and the later write wins silently.
func TestAFieldIsNeverFilledTwice(t *testing.T) {
	fields := []browser.Element{el(1, "", "name", "Name")}
	used := map[int]bool{}
	idx, ok := findField(fields, "given_name", used)
	if !ok {
		t.Skip("the first field did not match; nothing to guard against here")
	}
	used[idx] = true
	if _, again := findField(fields, "cc-name", used); again {
		t.Fatal("the same input was handed out twice, so one value would silently overwrite the other")
	}
}

// The card fields and the personal fields share one namespace in fieldHints.
// A catalog key colliding with a card kind would make the card fill
// unpredictable, and it would do so silently.
func TestCatalogKeysDoNotCollideWithCardFields(t *testing.T) {
	cardKinds := map[string]bool{"cc-number": true, "cc-exp": true, "cc-csc": true, "cc-name": true}
	for _, spec := range vault.Catalog {
		if cardKinds[spec.Key] {
			t.Fatalf("catalog key %q collides with a card field of the same name", spec.Key)
		}
	}
}

// Every catalog field that claims it can be autofilled must actually be
// reachable by the matcher, or the settings screen offers a box that quietly
// does nothing. This is the built-but-not-wired check, run as a test.
func TestEveryAutofillableDetailIsReachable(t *testing.T) {
	for _, spec := range vault.Catalog {
		if len(spec.Autofill) == 0 {
			continue
		}
		if len(fieldHints[spec.Key]) == 0 {
			t.Fatalf("%q offers autofill hints but the matcher has none, so filling it would never happen", spec.Key)
		}
	}
}

// The spoken password must never be releasable. It proves the boss is the boss;
// a Jarvis willing to say it out loud has handed over the only thing it does.
func TestSpokenPasswordCanNeverBeReleased(t *testing.T) {
	spec, ok := vault.SpecFor("passphrase")
	if !ok {
		t.Fatal("the spoken password is missing from the catalog")
	}
	if spec.Releasable {
		t.Fatal("the spoken password is marked releasable, so Jarvis could read it out")
	}
	if len(spec.Autofill) > 0 {
		t.Fatal("the spoken password has autofill hints, so it could be typed into a web form")
	}
	if !spec.Sealed {
		t.Fatal("the spoken password is stored in the clear")
	}
}
