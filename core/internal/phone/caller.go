// caller.go — who is actually on the line.
//
// The SIP From header is an ADDRESS, not a number:
//
//	"Kai" <sip:+16095003990@pstn.twilio.com>;tag=5697834693
//
// Scraping the trailing digits of that whole string returns the ;tag= random,
// not the caller. That is not theoretical: an inbound call from the boss's own
// cell was filed under 569-783-4693, his known-number entry never matched, and
// he was screened like a stranger on his own line. The caller lives in exactly
// one place — the user part of the URI — so parse it there, once, and let every
// consumer (recognition, call history, the agent's Caller ID line) share it.
package phone

import "strings"

// minCallerDigits is the shortest run of digits we will treat as a real number.
// Below this we are looking at a SIP extension or noise, not something dialable,
// and claiming to recognize it would be worse than admitting we don't.
const minCallerDigits = 7

// parseCallerNumber extracts the dialable caller number from a raw SIP From
// header, in E.164-ish "+digits" form. Returns "" when the caller is anonymous
// or the address carries no number — an honest "unknown", never a guess.
func parseCallerNumber(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Prefer the addr-spec inside angle brackets; a display name ("Kai +1 Cell")
	// can itself contain digits and must never be mistaken for the number.
	if i := strings.Index(s, "<"); i >= 0 {
		s = s[i+1:]
		if j := strings.Index(s, ">"); j >= 0 {
			s = s[:j]
		}
	}

	// Drop URI/header parameters (;tag=, ;user=phone) and any query string.
	// This is the cut that was missing: ;tag= is where the phantom number came from.
	if i := strings.IndexAny(s, ";?"); i >= 0 {
		s = s[:i]
	}

	// Strip the scheme.
	lower := strings.ToLower(s)
	for _, scheme := range []string{"sips:", "sip:", "tel:"} {
		if strings.HasPrefix(lower, scheme) {
			s = s[len(scheme):]
			break
		}
	}

	// The host is not the caller: sip:+16095003990@pstn.twilio.com.
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}

	// A user part can still carry visual separators: +1 (609) 500-3990.
	digits := onlyDigits(s)
	if len(digits) < minCallerDigits {
		return ""
	}
	return "+" + digits
}

// onlyDigits keeps the digits of s and drops everything else.
func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
