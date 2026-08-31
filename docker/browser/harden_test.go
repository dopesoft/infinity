package main

import "testing"

// bot.sannysoft.com, 2026-08-31: every check passed except "WebDriver (New):
// present (failed)", and the User-Agent was a hardcoded literal still claiming
// Chrome/124 long after the image had moved on. A UA that disagrees with its
// own Sec-CH-UA client hints is a stronger signal than an honest one, so the
// UA is derived from the running browser and the hints are built from it.

func TestDeHeadlessSwapsOnlyTheProductToken(t *testing.T) {
	const headless = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/131.0.6778.85 Safari/537.36"
	const want = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.6778.85 Safari/537.36"

	got := deHeadless(headless)
	if got != want {
		t.Fatalf("deHeadless:\n got %q\nwant %q", got, want)
	}
}

func TestDeHeadlessLeavesAHeadfulUAAlone(t *testing.T) {
	const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.6778.85 Safari/537.36"
	if got := deHeadless(ua); got != ua {
		t.Fatalf("a headful UA was rewritten:\n got %q\nwant %q", got, ua)
	}
}

func TestDeHeadlessPreservesTheRealVersion(t *testing.T) {
	// The version must never be invented. Whatever Chromium reports is what
	// ships, because that is the half that has to agree with the client hints.
	for _, v := range []string{"124.0.0.0", "131.0.6778.85", "140.0.7259.2"} {
		ua := "Mozilla/5.0 HeadlessChrome/" + v + " Safari/537.36"
		got := deHeadless(ua)
		if want := "Mozilla/5.0 Chrome/" + v + " Safari/537.36"; got != want {
			t.Fatalf("version %s mangled:\n got %q\nwant %q", v, got, want)
		}
	}
}

func TestChromeMajorMatchesTheUA(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want string
	}{
		{"full version", "Mozilla/5.0 Chrome/131.0.6778.85 Safari/537.36", "131"},
		{"major only", "Mozilla/5.0 Chrome/124 Safari/537.36", "124"},
		{"three digit", "Mozilla/5.0 Chrome/140.0.7259.2 Safari/537.36", "140"},
		// No recognisable version means no metadata rather than a guess: hints
		// that disagree with the UA are worse than no hints at all.
		{"no chrome token", "Mozilla/5.0 (X11; Linux x86_64) Firefox/128.0", ""},
		{"empty", "", ""},
		{"trailing token", "Mozilla/5.0 Chrome/", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chromeMajor(c.ua); got != c.want {
				t.Fatalf("chromeMajor(%q) = %q, want %q", c.ua, got, c.want)
			}
		})
	}
}

func TestDerivedUAAndHintsAgree(t *testing.T) {
	// The invariant the whole exercise exists for: whatever UA we advertise,
	// the brand version in Sec-CH-UA must be the same number.
	const reported = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/131.0.6778.85 Safari/537.36"
	ua := deHeadless(reported)
	major := chromeMajor(ua)
	if major != "131" {
		t.Fatalf("major = %q, want 131", major)
	}
	if want := "Chrome/" + major; !contains(ua, want) {
		t.Fatalf("UA %q does not carry %q, so the client hints would contradict it", ua, want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
