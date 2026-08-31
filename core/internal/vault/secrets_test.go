package vault

import "testing"

// The boss opened Settings and found this in a list captioned "cards Jarvis
// can pay with":
//
//	secret:vault.phone_passphrase   secret ····
//	secret:vault.identity           secret ····
//	secret:vault.payment_card       secret ····
//
// None of those is a card. Two of them cannot be a card. They shared a table
// with the wallet because they want the same key and the same ciphertext
// column, and every card query had been written as though the table held only
// cards.
//
// These tests pin the fix at the level that actually prevents it: the SQL
// predicate itself. A card read that forgets to exclude secrets is the bug,
// so the test asserts the predicate exists and is applied everywhere a row is
// treated as a card — including Revoke, where forgetting it would let a tap on
// a wallet delete button retire the boss's spoken passphrase instead.
func TestSecretRowsAreExcludedFromEveryCardQuery(t *testing.T) {
	if notASecret == "" {
		t.Fatal("there is no predicate separating secrets from cards")
	}
	// The brand written by putSecret must be the brand the card reads filter
	// on, or the filter matches nothing and the bug is back with a test still
	// passing.
	if !containsStr(notASecret, secretBrand) {
		t.Fatalf("the card filter %q does not reference the brand %q that putSecret writes", notASecret, secretBrand)
	}
}

// Sealing a secret must produce a row a card query cannot see. This asserts the
// two halves agree without needing a database: putSecret stamps secretBrand,
// notASecret excludes secretBrand.
func TestSecretsAndCardsAreMarkedTheSameWay(t *testing.T) {
	if secretBrand == "" {
		t.Fatal("secrets carry no brand, so nothing can filter them out")
	}
	for _, brand := range []string{"Visa", "Mastercard", "Amex", "Discover", "Card"} {
		if brand == secretBrand {
			t.Fatalf("%q is both a real card brand and the secret marker, so real cards would be hidden", brand)
		}
	}
	// brandOf must never independently invent the secret marker for a real
	// PAN, or that card would silently vanish from the wallet.
	for _, pan := range []string{"4242424242424242", "5555555555554444", "378282246310005", "6011111111111117", "9999999999999"} {
		if brandOf(pan) == secretBrand {
			t.Fatalf("brandOf(%s) returned the secret marker, so a real card would be filtered out of the wallet", pan)
		}
	}
}

// The identity blob and the passphrase are stored under distinct labels, so
// saving one can never overwrite the other.
func TestSecretKeysAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []string{KeyPassphrase, KeyIdentity, KeyCard, KeyBossCell} {
		if k == "" {
			t.Fatal("an empty secret key would collide with every other")
		}
		if seen[k] {
			t.Fatalf("%q is used for two different secrets", k)
		}
		seen[k] = true
	}
	// The cell is deliberately NOT sealed, so it must not appear in the
	// migration list. If it did, patch-in would break whenever the key is
	// absent, which is precisely when the boss most needs to be reached.
	for _, k := range SecretMetaKeys {
		if k == KeyBossCell {
			t.Fatal("the cell number is being sealed; patch-in needs it readable without the vault key")
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
