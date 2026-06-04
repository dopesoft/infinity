package dashboard

import (
	"testing"
	"time"
)

func TestParseDueAtAcceptsDateOnlyAtNoonUTC(t *testing.T) {
	got, err := parseDueAt("2026-06-10")
	if err != nil {
		t.Fatalf("parseDueAt returned error: %v", err)
	}
	if got == nil {
		t.Fatal("parseDueAt returned nil")
	}
	want := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parseDueAt = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestParseDueAtAcceptsRFC3339(t *testing.T) {
	got, err := parseDueAt("2026-06-10T09:30:00-05:00")
	if err != nil {
		t.Fatalf("parseDueAt returned error: %v", err)
	}
	if got == nil || got.Format(time.RFC3339) != "2026-06-10T09:30:00-05:00" {
		t.Fatalf("unexpected parsed time: %#v", got)
	}
}

func TestParseDueAtEmptyClearsDate(t *testing.T) {
	got, err := parseDueAt("")
	if err != nil {
		t.Fatalf("parseDueAt returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("parseDueAt empty = %#v, want nil", got)
	}
}

func TestParseDueAtRejectsInvalidDate(t *testing.T) {
	if got, err := parseDueAt("June 10"); err == nil || got != nil {
		t.Fatalf("parseDueAt invalid = %#v, %v; want nil error", got, err)
	}
}
