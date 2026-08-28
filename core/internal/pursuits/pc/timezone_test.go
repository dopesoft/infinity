package pc

import (
	"testing"
	"time"
)

// The day boundary is computed twice: in Go (the coach's phase derivation) and
// in SQL (the missed-day count, via AT TIME ZONE). Both must be handed the SAME
// zone string. If they diverge, the two are hours apart and a day the boss
// actually logged gets counted as missed - the one thing the non-shaming design
// promises will never happen.
func TestNormalizeTimezoneIsTheSingleDayBoundary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "blank falls back to the boss's zone", in: "", want: DefaultTimezone},
		{name: "whitespace is blank", in: "   ", want: DefaultTimezone},
		{name: "a real zone passes through", in: "America/Chicago", want: "America/Chicago"},
		{name: "another real zone passes through", in: "Europe/London", want: "Europe/London"},
		{name: "UTC passes through", in: "UTC", want: "UTC"},
		{name: "an unloadable zone degrades to UTC", in: "Mars/Olympus_Mons", want: "UTC"},
		{name: "a city without a region degrades to UTC", in: "Chicago", want: "UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTimezone(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeTimezone(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// Whatever it returns must be loadable, because that exact string
			// is what gets handed to Postgres. A value Go accepts but Postgres
			// rejects would fail every cockpit read with "time zone not
			// recognized" and leave the surface dead.
			if _, err := time.LoadLocation(got); err != nil {
				t.Fatalf("NormalizeTimezone returned %q, which does not load: %v", got, err)
			}
		})
	}
}

// Go and SQL have to resolve the same instant to the same civil date. This
// pins the Go half: loadLocation must always agree with the string
// NormalizeTimezone hands to Postgres, including for a broken stored value.
func TestLoadLocationAgreesWithTheNormalisedZone(t *testing.T) {
	for _, tz := range []string{"", "   ", "America/Chicago", "Europe/London", "Mars/Olympus_Mons", "Chicago", "UTC"} {
		loc := loadLocation(tz)
		if loc == nil {
			t.Fatalf("loadLocation(%q) returned nil", tz)
		}
		if loc.String() != NormalizeTimezone(tz) {
			t.Fatalf("loadLocation(%q) resolved to %q but SQL would be sent %q",
				tz, loc.String(), NormalizeTimezone(tz))
		}
	}
}

// A bad zone must be refused where it is WRITTEN, not absorbed where it is
// read. Stored, it silently moves every future day boundary for the pursuit,
// and the boss experiences that as the programme counting days wrong rather
// than as the typo it is.
func TestIsValidTimezoneGuardsTheWriteBoundary(t *testing.T) {
	for _, good := range []string{"America/Chicago", "UTC", "Europe/London", "Asia/Tokyo"} {
		if !IsValidTimezone(good) {
			t.Fatalf("%q is a real IANA zone and must be accepted", good)
		}
	}
	// Deliberately no case-variant case here ("america/chicago"): the macOS
	// zoneinfo directory is case-insensitive while the embedded tzdata the
	// container uses is not, so asserting either way would pass on one target
	// and fail on the other.
	for _, bad := range []string{"Chicago", "CST", "GMT+5", "Mars/Olympus_Mons"} {
		if IsValidTimezone(bad) {
			t.Fatalf("%q is not a loadable IANA zone and must be rejected at the write boundary", bad)
		}
	}
}

// The runtime image is distroless and carries no /usr/share/zoneinfo, so the
// binary embeds tzdata. Without it every LoadLocation fails, the whole
// programme silently runs on UTC, and "day 5 of 21" rolls over at 7pm Chicago
// while Postgres - which has its own tzdata - still says it is day 5. This
// asserts the zone the boss actually lives in resolves to a real offset.
func TestBossTimezoneResolvesToARealOffset(t *testing.T) {
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		t.Fatalf("%s must be loadable (is _ \"time/tzdata\" still imported in cmd/infinity?): %v",
			DefaultTimezone, err)
	}
	// Late evening in Chicago is already the next day in UTC. If tzdata were
	// missing this would collapse to the same date and every evening session
	// would be filed against tomorrow.
	evening := time.Date(2026, time.August, 26, 23, 30, 0, 0, loc)
	if evening.UTC().Day() == evening.Day() {
		t.Fatal("Chicago 11:30pm and UTC must fall on different dates; tzdata looks unavailable")
	}
	if got := timeToLocalDate(evening, loc).Day(); got != 26 {
		t.Fatalf("11:30pm Chicago belongs to the 26th, got day %d", got)
	}
}

// Day arithmetic must survive a pursuit whose stored zone is unusable. The
// cockpit still has to render - degraded to UTC, consistently on both sides -
// rather than erroring out and leaving the boss with a dead screen.
func TestDeriveDaySurvivesAnUnusableStoredZone(t *testing.T) {
	start := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	if got := DeriveDay(start, now, "Mars/Olympus_Mons", 21); got != 5 {
		t.Fatalf("DeriveDay with an unusable zone = %d, want 5 (UTC-degraded but still counting)", got)
	}
}
