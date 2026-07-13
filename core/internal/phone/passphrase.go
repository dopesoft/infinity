// passphrase.go — boss verification over a lossy channel.
//
// The passphrase is the ONLY thing that turns a voice on the line into the
// boss, so it is checked here in code and never handed to the call agent (which
// could be sweet-talked out of it). But it arrives through a phone codec and a
// speech-to-text model, and that path is lossy by nature: "Glock 17" was
// transcribed "Block 17", the exact-substring check missed by one letter, the
// boss was never verified, and his instructions were silently dropped while the
// call was filed green.
//
// So matching tolerates transcription drift (edit distance), never semantic
// drift: one edit per five characters, so a mis-heard consonant passes and a
// different phrase does not. Everything else about verification stays strict.
package phone

import "strings"

// editsPerChars sets the tolerance: 1 permitted edit per 5 normalized
// characters, floor 1. "glock17" (7) tolerates 1 -> "block17" verifies.
// "bluefalcon" (10) tolerates 2.
const editsPerChars = 5

// nearMissFactor widens the band that counts as an ATTEMPT (someone reaching
// for the passphrase and missing) without verifying them. An attempt that fails
// is the loudest signal we have that a caller is either the boss with a bad
// phrase or someone impersonating him, and it must never pass silently.
const nearMissFactor = 2

// normalizeSpeech reduces spoken text to comparable form: lowercase letters and
// digits only. Casing, punctuation and spacing are transcription artifacts, not
// content ("Blue Falcon!" and "blue falcon" are the same phrase).
func normalizeSpeech(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tolerance returns the permitted edit distance for a phrase of n characters.
func tolerance(n int) int {
	if t := n / editsPerChars; t > 1 {
		return t
	}
	return 1
}

// matchPassphrase scans one spoken line for the passphrase.
//
// It returns the line with the matched words replaced by "[passphrase]" (so the
// secret never reaches storage, the transcript, the summarizer, or a push),
// whether the caller VERIFIED, and whether they merely ATTEMPTED it (a near
// miss). A verified match is always also an attempt.
//
// Matching walks word windows rather than characters so the redaction lands on
// whole words and a same-breath instruction survives it: "It's Glock 17, call my
// wife" redacts to "It's [passphrase], call my wife" — the ask is preserved.
func matchPassphrase(text, phrase string) (redacted string, verified, attempted bool) {
	want := normalizeSpeech(phrase)
	words := strings.Fields(text)
	if want == "" || len(words) == 0 {
		return text, false, false
	}

	tol := tolerance(len(want))
	near := tol * nearMissFactor

	// A phrase spoken aloud can pick up a filler word or lose one, so allow the
	// window to breathe either side of the phrase's own word count.
	maxWindow := len(strings.Fields(phrase)) + 2

	bestDist, bestStart, bestEnd := -1, -1, -1
	for i := range words {
		for w := 1; w <= maxWindow && i+w <= len(words); w++ {
			cand := normalizeSpeech(strings.Join(words[i:i+w], " "))
			if cand == "" {
				continue
			}
			// Cheap prune: a window that differs in LENGTH by more than the
			// near-miss band cannot land inside it.
			if abs(len(cand)-len(want)) > near {
				continue
			}
			if d := levenshtein(cand, want); bestDist < 0 || d < bestDist {
				bestDist, bestStart, bestEnd = d, i, i+w
			}
		}
	}
	if bestDist < 0 {
		return text, false, false
	}
	if bestDist > tol {
		// Missed. Still report a near miss so the call cannot end quietly.
		return text, false, bestDist <= near
	}

	// Carry the last matched word's trailing punctuation onto the placeholder,
	// so "It's Glock 17, call my wife" redacts to "It's [passphrase], call my
	// wife" and the sentence the boss actually spoke still reads like one.
	placeholder := "[passphrase]" + trailingPunct(words[bestEnd-1])

	out := make([]string, 0, len(words))
	out = append(out, words[:bestStart]...)
	out = append(out, placeholder)
	out = append(out, words[bestEnd:]...)
	return strings.Join(out, " "), true, true
}

// trailingPunct returns the run of non-alphanumeric characters at the end of a
// word ("17," -> ",").
func trailingPunct(word string) string {
	r := []rune(word)
	i := len(r)
	for i > 0 {
		c := r[i-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			break
		}
		i--
	}
	return string(r[i:])
}

// spokenAsk strips the redaction placeholder from a verified line and reports
// what is LEFT — the instruction the boss gave in the same breath as the
// phrase. Empty when he only said the passphrase.
func spokenAsk(redacted string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(
		strings.ReplaceAll(redacted, "[passphrase]", " ")), " "))
}

// levenshtein is the standard edit distance, two rows.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
