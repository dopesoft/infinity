package vault

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A vault is only worth having if the sealed bytes are useless without the key
// and if a missing key stops everything rather than degrading quietly. Both are
// tested here, because both are the difference between this and the plaintext
// rows in infinity_meta it replaces.

func testStore(t *testing.T) *Store {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	t.Setenv("INFINITY_VAULT_KEY", base64.StdEncoding.EncodeToString(key))
	return &Store{key: loadKey()}
}

func TestSealedBytesDoNotContainTheCard(t *testing.T) {
	s := testStore(t)
	const pan = "4242424242424242"
	sealed, nonce, err := s.seal(Secrets{PAN: pan, CVC: "737"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(string(sealed), pan) || strings.Contains(string(sealed), "737") {
		t.Fatal("the card is readable in the sealed bytes; a database dump would be a card dump")
	}
	if len(nonce) == 0 {
		t.Fatal("no nonce")
	}
}

func TestRoundTrip(t *testing.T) {
	s := testStore(t)
	in := Secrets{PAN: "4242424242424242", CVC: "737", Name: "K", Billing: map[string]string{"zip": "77002"}}
	sealed, nonce, err := s.seal(in)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	out, err := s.open(sealed, nonce)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if out.PAN != in.PAN || out.CVC != in.CVC || out.Billing["zip"] != "77002" {
		t.Fatalf("round trip lost data: %+v", out)
	}
}

func TestNonceIsNeverReused(t *testing.T) {
	s := testStore(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		_, nonce, err := s.seal(Secrets{PAN: "4242424242424242"})
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if seen[string(nonce)] {
			t.Fatal("a nonce repeated; GCM leaks catastrophically on nonce reuse")
		}
		seen[string(nonce)] = true
	}
}

func TestATamperedRowWillNotOpen(t *testing.T) {
	// GCM is authenticated, so a row someone edited must fail loudly rather
	// than decrypt to something plausible.
	s := testStore(t)
	sealed, nonce, err := s.seal(Secrets{PAN: "4242424242424242"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	sealed[0] ^= 0xFF
	if _, err := s.open(sealed, nonce); err == nil {
		t.Fatal("a tampered row opened; the ciphertext is not authenticated")
	}
}

func TestTheWrongKeyWillNotOpen(t *testing.T) {
	s := testStore(t)
	sealed, nonce, err := s.seal(Secrets{PAN: "4242424242424242"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(200 - i)
	}
	s2 := &Store{key: other}
	if _, err := s2.open(sealed, nonce); err == nil {
		t.Fatal("a different key opened the card")
	}
}

func TestNoKeyMeansFailClosed(t *testing.T) {
	// The rule the whole package hangs on: no key is "the vault is
	// unavailable", never "there is no card". Callers must stop.
	t.Setenv("INFINITY_VAULT_KEY", "")
	s := &Store{key: loadKey()}
	if s.Healthy() {
		t.Fatal("a keyless vault reported healthy, so a purchase would proceed without one")
	}
	if _, _, err := s.seal(Secrets{PAN: "4242424242424242"}); err != ErrNoKey {
		t.Fatalf("seal without a key returned %v, want ErrNoKey", err)
	}
	if _, err := s.open([]byte("x"), []byte("y")); err != ErrNoKey {
		t.Fatalf("open without a key returned %v, want ErrNoKey", err)
	}
}

func TestShortKeyIsRejected(t *testing.T) {
	// A 16-byte key would silently give AES-128 where the whole design says
	// AES-256. Refusing is better than quietly being weaker than advertised.
	t.Setenv("INFINITY_VAULT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if k := loadKey(); k != nil {
		t.Fatalf("accepted a %d-byte key", len(k))
	}
}

func TestKeyAcceptsHexOrBase64(t *testing.T) {
	t.Setenv("INFINITY_VAULT_KEY", strings.Repeat("ab", 32)) // 32 bytes as hex
	if k := loadKey(); len(k) != 32 {
		t.Fatalf("hex key not accepted, got %d bytes", len(k))
	}
}

func TestDisplayFieldsCarryNothingSpendable(t *testing.T) {
	if got := last4("4242424242424242"); got != "4242" {
		t.Fatalf("last4 = %q", got)
	}
	if got := brandOf("4242424242424242"); got != "Visa" {
		t.Fatalf("brandOf = %q", got)
	}
	if got := brandOf("5412341234123456"); got != "Mastercard" {
		t.Fatalf("brandOf = %q", got)
	}
	if got := digitsOnly("4242 4242-4242 4242"); got != "4242424242424242" {
		t.Fatalf("digitsOnly = %q", got)
	}
}
