package phone

import "testing"

// The regression that started this: an inbound call from the boss's own cell
// (+1 609 500 3990) was filed under 569-783-4693 — digits scraped off the SIP
// ;tag= — so lookupCaller never matched his known-number entry and he was
// screened like a stranger on his own line.
func TestParseCallerNumber(t *testing.T) {
	cases := []struct {
		name string
		from string
		want string
	}{
		{
			name: "sip uri with display name and tag (the regression)",
			from: `"Kai" <sip:+16095003990@pstn.twilio.com>;tag=5697834693`,
			want: "+16095003990",
		},
		{
			name: "bare angle-bracket uri",
			from: "<sip:+16095003990@example.com>",
			want: "+16095003990",
		},
		{
			name: "no angle brackets, with tag",
			from: "sip:+16095003990@example.com;tag=abc123",
			want: "+16095003990",
		},
		{
			name: "tel uri",
			from: "tel:+16095003990",
			want: "+16095003990",
		},
		{
			name: "user part carries visual separators",
			from: "<sip:+1 (609) 500-3990@example.com>;tag=99",
			want: "+16095003990",
		},
		{
			name: "sips scheme",
			from: "<sips:16095003990@secure.example.com>",
			want: "+16095003990",
		},
		{
			name: "display name containing digits must not win",
			from: `"Kai 2nd Line" <sip:+16095003990@example.com>;tag=7777777777`,
			want: "+16095003990",
		},
		{
			name: "host containing digits must not win",
			from: "<sip:+16095003990@10.20.30.40:5060>;tag=1234567890",
			want: "+16095003990",
		},
		{
			name: "bare number",
			from: "+16095003990",
			want: "+16095003990",
		},
		// An anonymous caller must come back UNKNOWN, never a number invented
		// from the domain — a wrong "recognized" caller is worse than none.
		{name: "anonymous", from: "<sip:anonymous@anonymous.invalid>;tag=abc", want: ""},
		{name: "empty", from: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCallerNumber(tc.from); got != tc.want {
				t.Fatalf("parseCallerNumber(%q) = %q, want %q", tc.from, got, tc.want)
			}
		})
	}
}

// The old code path, kept as a guard: whatever we parse must survive the
// last-10-digits keying used for known_numbers and call history.
func TestParseCallerNumberKeysConsistently(t *testing.T) {
	const from = `"Kai" <sip:+16095003990@pstn.twilio.com>;tag=5697834693`
	if got := lastDigits(parseCallerNumber(from), 10); got != "6095003990" {
		t.Fatalf("history key = %q, want 6095003990", got)
	}
}
