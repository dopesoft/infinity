package phone

import "testing"

// The exact failure: the boss said "Glock 17", the transcriber heard "Block 17",
// the substring check missed, and every instruction he gave was dropped.
func TestMatchPassphraseToleratesTranscriptionDrift(t *testing.T) {
	cases := []struct {
		name          string
		spoken        string
		phrase        string
		wantVerified  bool
		wantAttempted bool
		wantRedacted  string
	}{
		{
			name:          "mis-heard consonant verifies (the regression)",
			spoken:        "It's Block 17.",
			phrase:        "Glock 17",
			wantVerified:  true,
			wantAttempted: true,
			wantRedacted:  "It's [passphrase].",
		},
		{
			name:          "exact match",
			spoken:        "It's blue falcon.",
			phrase:        "blue falcon",
			wantVerified:  true,
			wantAttempted: true,
			wantRedacted:  "It's [passphrase].",
		},
		{
			name:          "casing and punctuation are transcription artifacts",
			spoken:        "Blue Falcon!",
			phrase:        "blue falcon",
			wantVerified:  true,
			wantAttempted: true,
			wantRedacted:  "[passphrase]!",
		},
		{
			name:          "one mis-heard word still verifies",
			spoken:        "The passphrase is blue falcons",
			phrase:        "blue falcon",
			wantVerified:  true,
			wantAttempted: true,
			wantRedacted:  "The passphrase is [passphrase]",
		},
		// The whole point of the fix: an instruction given in the SAME BREATH as
		// the phrase must survive redaction, or we verify the boss and then throw
		// away what he actually asked for.
		{
			name:          "same-breath instruction survives redaction",
			spoken:        "It's Glock 17, call my wife and read her a poem",
			phrase:        "Glock 17",
			wantVerified:  true,
			wantAttempted: true,
			wantRedacted:  "It's [passphrase], call my wife and read her a poem",
		},
		// Tolerance must not become a skeleton key.
		{
			name:          "a different phrase does not verify",
			spoken:        "My name is Robert and I'm calling about the invoice",
			phrase:        "blue falcon",
			wantVerified:  false,
			wantAttempted: false,
		},
		{
			name:          "wrong passphrase is a near miss, not a pass",
			spoken:        "It's red falcon",
			phrase:        "blue falcon",
			wantVerified:  false,
			wantAttempted: true,
		},
		{
			name:          "no passphrase configured verifies nobody",
			spoken:        "It's blue falcon",
			phrase:        "",
			wantVerified:  false,
			wantAttempted: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redacted, verified, attempted := matchPassphrase(tc.spoken, tc.phrase)
			if verified != tc.wantVerified {
				t.Fatalf("verified = %v, want %v (spoken %q, phrase %q)", verified, tc.wantVerified, tc.spoken, tc.phrase)
			}
			if attempted != tc.wantAttempted {
				t.Fatalf("attempted = %v, want %v", attempted, tc.wantAttempted)
			}
			if tc.wantRedacted != "" && redacted != tc.wantRedacted {
				t.Fatalf("redacted = %q, want %q", redacted, tc.wantRedacted)
			}
		})
	}
}

// The secret must never survive into anything durable: transcript, summary,
// push, call history.
func TestMatchPassphraseRedactsTheSecret(t *testing.T) {
	for _, spoken := range []string{"It's Glock 17.", "glock seventeen", "Blue Falcon"} {
		for _, phrase := range []string{"Glock 17", "blue falcon"} {
			redacted, verified, _ := matchPassphrase(spoken, phrase)
			if !verified {
				continue
			}
			if normalizeSpeech(redacted) != normalizeSpeech(redacted) {
				t.Fatal("unreachable")
			}
			if containsNormalized(redacted, phrase) {
				t.Fatalf("redacted %q still contains the passphrase %q", redacted, phrase)
			}
		}
	}
}

func containsNormalized(text, phrase string) bool {
	n, p := normalizeSpeech(text), normalizeSpeech(phrase)
	return p != "" && len(n) >= len(p) && contains(n, p)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestSpokenAsk(t *testing.T) {
	if got := spokenAsk("It's [passphrase], call my wife"); got != "It's , call my wife" && got != "It's, call my wife" {
		// Placeholder removal leaves the surrounding words intact; exact spacing
		// is not load-bearing, presence of the instruction is.
		if !contains(got, "call my wife") {
			t.Fatalf("spokenAsk lost the instruction: %q", got)
		}
	}
	if got := spokenAsk("[passphrase]"); got != "" {
		t.Fatalf("phrase-only line should leave no ask, got %q", got)
	}
}
