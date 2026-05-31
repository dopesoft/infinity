package maintenance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseOptionsAppliesOverrides(t *testing.T) {
	raw := json.RawMessage(`{
		"reflect_window": "48h",
		"reflect_limit": 7,
		"compress": false,
		"compress_batch": 12,
		"gym_limit": 33
	}`)
	opts := ParseOptions(raw)
	if opts.ReflectWindow != 48*time.Hour {
		t.Fatalf("ReflectWindow = %s", opts.ReflectWindow)
	}
	if opts.ReflectLimit != 7 || opts.Compress || opts.CompressBatch != 12 || opts.GymLimit != 33 {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}

func TestParseOptionsKeepsDefaultsOnBadInput(t *testing.T) {
	opts := ParseOptions(json.RawMessage(`{"reflect_window":"nope","reflect_limit":0}`))
	def := DefaultOptions()
	if opts.ReflectWindow != def.ReflectWindow || opts.ReflectLimit != def.ReflectLimit {
		t.Fatalf("expected defaults, got %+v", opts)
	}
}

func TestStageErrorFormatting(t *testing.T) {
	err := StageError{Stage: "gym_extract", Message: "cannot extract elements from a scalar"}
	if got := err.Error(); got != "gym_extract: cannot extract elements from a scalar" {
		t.Fatalf("unexpected error string %q", got)
	}
}

func TestReportErrorSummary(t *testing.T) {
	report := Report{Errors: []StageError{
		{Stage: "reflection_chains", Message: "cannot get array length of a scalar"},
		{Stage: "gym_extract", Message: "cannot extract elements from a scalar"},
	}}
	got := report.ErrorSummary()
	if !strings.Contains(got, "reflection_chains: cannot get array length of a scalar") {
		t.Fatalf("missing first stage in summary: %q", got)
	}
	if !strings.Contains(got, "gym_extract: cannot extract elements from a scalar") {
		t.Fatalf("missing second stage in summary: %q", got)
	}
}
