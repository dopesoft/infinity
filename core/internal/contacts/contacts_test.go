package contacts

import "testing"

func TestNormalize(t *testing.T) {
	// The boss says a name; a transcriber punctuates it however it likes. These
	// must all land on the same contact.
	for _, spoken := range []string{"Goodfellas Pizza", "goodfellas pizza", "Goodfella's Pizza!", "GOODFELLAS  PIZZA"} {
		if got := Normalize(spoken); got != "goodfellaspizza" {
			t.Fatalf("Normalize(%q) = %q, want goodfellaspizza", spoken, got)
		}
	}
}

func TestDescribeIsSpeakable(t *testing.T) {
	// Describe is read ALOUD to the boss mid-call, so it has to be plain
	// language, and it has to distinguish two branches of the same place, which
	// is the whole reason he gets asked "the one on Preston Road?".
	out := Describe([]Contact{
		{Name: "Goodfellas Pizza", Number: "+12145550111", Kind: "org", Location: "the one on Preston Road"},
		{Name: "Goodfellas Pizza", Number: "+12145550222", Kind: "org", Location: "the one on Legacy Drive"},
	})
	for _, want := range []string{"Preston Road", "Legacy Drive", "+12145550111", "+12145550222"} {
		if !contains(out, want) {
			t.Fatalf("Describe() omitted %q, so the boss could not be asked which he meant:\n%s", want, out)
		}
	}

	// An empty book must SAY it is empty, never return something that reads like
	// a result. A blank answer here becomes a guessed number on a live call.
	if got := Describe(nil); got == "" {
		t.Fatal("an empty lookup must say so, never come back blank")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestLastDigits(t *testing.T) {
	// Inbound recognition keys on the last 10 digits, so every way a number can
	// be written still finds the same person.
	for _, form := range []string{"+19293100906", "929-310-0906", "(929) 310 0906", "9293100906"} {
		if got := lastDigits(form, 10); got != "9293100906" {
			t.Fatalf("lastDigits(%q) = %q, want 9293100906", form, got)
		}
	}
}

// A contact saved as "929-310-0906" reads perfectly in the book and then fails
// the E.164 check at dial time: a contact that exists, looks right, and cannot
// be called. Normalizing on the way IN is what stops that.
func TestNormalizeNumber(t *testing.T) {
	for _, spoken := range []string{"929-310-0906", "(929) 310 0906", "9293100906", "+1 929 310 0906", "1-929-310-0906"} {
		if got := NormalizeNumber(spoken); got != "+19293100906" {
			t.Fatalf("NormalizeNumber(%q) = %q, want +19293100906", spoken, got)
		}
	}
	// Not dialable: refuse, rather than store something that breaks at dial time.
	for _, junk := range []string{"", "ask her", "12", "extension 4"} {
		if got := NormalizeNumber(junk); got != "" {
			t.Fatalf("NormalizeNumber(%q) = %q, want refusal", junk, got)
		}
	}
}
