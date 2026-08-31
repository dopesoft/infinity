package memory

import "testing"

// browser_observe's output becomes an observation's raw_text, which is embedded
// and stored, and the payload beside it holds the verbatim tool input. Before
// these patterns, a card number typed into any page was shipped to the
// embedding provider and written into the memory graph in the clear. The phone
// transcript path had a card regex; nothing else did.
//
// The second half of the job is not over-redacting. An order number, a tracking
// code and a phone number are all long digit runs, and quietly destroying them
// would corrupt ordinary memories to fix a rare one. Luhn is what separates
// them.

func TestCardNumbersAreRedacted(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"plain visa", "the card is 4242424242424242"},
		{"spaced", "card 4242 4242 4242 4242 exp 12/28"},
		{"dashed", "4242-4242-4242-4242"},
		{"amex", "paid with 378282246310005"},
		{"mastercard", "5555555555554444 on file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, changed := StripSecrets(c.in)
			if !changed {
				t.Fatalf("nothing was redacted in %q", c.in)
			}
			for _, digit := range []string{"4242424242424242", "378282246310005", "5555555555554444"} {
				if contains(out, digit) {
					t.Fatalf("the card survived redaction: %q", out)
				}
			}
		})
	}
}

func TestOrdinaryLongNumbersSurvive(t *testing.T) {
	// Every one of these fails Luhn, which is exactly why it is safe to keep
	// them. Redacting an order id would make a receipt useless.
	//
	// Note what this does NOT claim: a 13-19 digit number that happens to pass
	// Luhn IS redacted, because at that point it is genuinely indistinguishable
	// from a card. That is the correct side to err on, and it is why the check
	// is Luhn rather than "looks long".
	for _, s := range []string{
		"order number 114-2938471-2938",
		"tracking 1Z999AA10123456784",
		"invoice INV-2026-0041",
		"call him on 12145550123",
	} {
		out, _ := StripSecrets(s)
		if out != s {
			t.Fatalf("redacted an ordinary number:\n in  %q\n out %q", s, out)
		}
	}
}

func TestSecurityCodesAreRedactedOnlyWithContext(t *testing.T) {
	out, _ := StripSecrets("cvv 737 and the zip")
	if contains(out, "737") {
		t.Fatalf("a labelled security code survived: %q", out)
	}
	// Three bare digits are far too common to redact on sight.
	keep := "there were 737 results and gate 42"
	if got, _ := StripSecrets(keep); got != keep {
		t.Fatalf("redacted a bare number with no card context:\n in  %q\n out %q", keep, got)
	}
}

func TestDeepRedactionReachesNestedPayloads(t *testing.T) {
	// The payload is where the verbatim tool input lives, which is where a
	// typed card number actually ends up.
	in := map[string]any{
		"tool": "browser_act",
		"input": map[string]any{
			"value": "4242424242424242",
			"label": "Card number",
		},
		"history": []any{
			map[string]any{"value": "5555555555554444"},
		},
	}
	out, ok := StripSecretsDeep(in).(map[string]any)
	if !ok {
		t.Fatal("StripSecretsDeep did not return a map")
	}
	nested := out["input"].(map[string]any)
	if contains(nested["value"].(string), "4242") {
		t.Fatalf("a card survived inside the payload: %v", nested["value"])
	}
	arr := out["history"].([]any)
	if contains(arr[0].(map[string]any)["value"].(string), "5555") {
		t.Fatal("a card survived inside an array in the payload")
	}
	// Non-secret fields must come through untouched, or the payload stops
	// being useful for debugging.
	if out["tool"] != "browser_act" {
		t.Fatalf("an ordinary field was mangled: %v", out["tool"])
	}
}

func TestLuhn(t *testing.T) {
	valid := []string{"4242424242424242", "5555555555554444", "378282246310005", "6011111111111117"}
	for _, v := range valid {
		if !luhn(v) {
			t.Fatalf("%s is a real card number and did not pass Luhn", v)
		}
	}
	invalid := []string{"4242424242424243", "1234567890123456", "", "abc", "1234567890123"}
	for _, v := range invalid {
		if luhn(v) {
			t.Fatalf("%q passed Luhn", v)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
