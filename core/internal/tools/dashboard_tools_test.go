package tools

import (
	"testing"
	"time"
)

func TestParseOptionalTimeDateOnlyUsesNoonUTC(t *testing.T) {
	got, err := parseOptionalTime("2026-06-10")
	if err != nil {
		t.Fatalf("parseOptionalTime returned error: %v", err)
	}
	if got == nil {
		t.Fatal("parseOptionalTime returned nil")
	}
	want := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parseOptionalTime = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestParseOptionalTimeEmptyMeansClear(t *testing.T) {
	got, err := parseOptionalTime("")
	if err != nil {
		t.Fatalf("parseOptionalTime returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("parseOptionalTime empty = %#v, want nil", got)
	}
}

func TestParseOptionalTimeRejectsInvalidDate(t *testing.T) {
	if got, err := parseOptionalTime("Decembuary 31"); err == nil || got != nil {
		t.Fatalf("parseOptionalTime invalid = %#v, %v; want nil error", got, err)
	}
}
