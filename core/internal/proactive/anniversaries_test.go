package proactive

import (
	"testing"
	"time"
)

// The boss's actual question: he wished Phumi a happy birthday on July 10 this
// year. Next year, that date must come back on its own.
func TestNextOccurrenceRollsForwardAYear(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) // three days after her birthday

	got, ok := nextOccurrence("1991-07-10", now)
	if !ok {
		t.Fatal("a birthday with a year in it must still parse")
	}
	// The YEAR is irrelevant: a birthday is a day of the year, forever.
	if got.Year() != 2027 || got.Month() != time.July || got.Day() != 10 {
		t.Fatalf("next occurrence = %s, want 2027-07-10", got.Format("2006-01-02"))
	}
}

func TestNextOccurrenceUpcoming(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	got, ok := nextOccurrence("July 17", now)
	if !ok {
		t.Fatal(`"July 17" must parse: the extractor writes dates the way people say them`)
	}
	if got.Month() != time.July || got.Day() != 17 || got.Year() != 2026 {
		t.Fatalf("next occurrence = %s, want 2026-07-17", got.Format("2006-01-02"))
	}
	if d := got.Sub(now); d < 0 || d > lookAhead {
		t.Fatalf("a date four days out must fall inside the look-ahead window, got %s", d)
	}
}

func TestNextOccurrenceFormats(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, form := range []string{"1991-07-10", "07-10", "Jul 10", "July 10", "10 July", "1991/07/10"} {
		got, ok := nextOccurrence(form, now)
		if !ok || got.Month() != time.July || got.Day() != 10 {
			t.Fatalf("nextOccurrence(%q) = %v, %v; want July 10", form, got, ok)
		}
	}
	// Junk must not become a phantom birthday.
	for _, junk := range []string{"", "soon", "next summer", "unknown"} {
		if _, ok := nextOccurrence(junk, now); ok {
			t.Fatalf("nextOccurrence(%q) must not invent a date", junk)
		}
	}
}

// It must not know what a birthday IS: any recurring occasion counts, which is
// what stops this becoming a bespoke birthday feature.
func TestIsOccasionKey(t *testing.T) {
	for _, k := range []string{"birthday", "birth_date", "Birthday", "wedding anniversary", "anniversary"} {
		if !isOccasionKey(k) {
			t.Fatalf("%q should be a recurring occasion", k)
		}
	}
	for _, k := range []string{"email", "employer", "phone", "last_seen"} {
		if isOccasionKey(k) {
			t.Fatalf("%q is not an occasion and must not raise a finding", k)
		}
	}
}

func TestWhenWords(t *testing.T) {
	when := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	if got := whenWords(24*time.Hour, when); got != "tomorrow" {
		t.Fatalf("whenWords = %q, want tomorrow", got)
	}
	if got := whenWords(0, when); got != "today" {
		t.Fatalf("whenWords = %q, want today", got)
	}
}
