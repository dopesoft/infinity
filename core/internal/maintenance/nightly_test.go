package maintenance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/dopesoft/infinity/core/internal/worldmodel"
)

// The digest must QUOTE the run's real artifacts in labeled sections the
// Studio modal renders as cards, with the counters translated to plain
// first-person English — never "115 decayed, 109 hot-reset" jargon.
func TestReportSummary_QuotesHighlights(t *testing.T) {
	r := Report{
		ReflectedSessions:      9,
		ReflectionChains:       8,
		CompressedObservations: 41,
		Consolidate: memory.ConsolidateReport{
			Decayed:              115,
			HotReset:             109,
			ClustersFound:        1,
			ProceduralReweighted: 2,
			Forget:               memory.ForgetReport{LowValue: 157},
		},
		TrainingExamples: plasticity.ExtractResult{Inserted: 9},
		WorldModel:       worldmodel.ExtractReport{Upserted: 93},
		Highlights: Highlights{
			Lessons: []string{
				"When the user names a skill, invoke skills_invoke directly before exploring adjacent planning tools.",
				"Do not unload required tools until the verification workflow is complete.",
			},
			Memories: []string{"Boss prefers chip-style scrollable page tabs"},
			Entities: []string{"Namecheap", "Railway"},
		},
	}
	got := r.Summary()

	for _, want := range []string{
		"Last night I went back over 9 of yesterday's sessions and came away with 2 new lessons, then tidied up my memory.",
		"What I learned:\n- When the user names a skill, invoke skills_invoke directly",
		"Worth remembering:\n- Boss prefers chip-style scrollable page tabs",
		"Now tracking: Namecheap, Railway",
		"Tidy-up: 41 raw notes turned into lasting memories, 115 stale memories left to fade, 109 memories refreshed because they came up again, 1 set of related memories grouped together, 2 skills re-ranked by how well they have been working, 157 memories I no longer need cleared out, 9 new examples saved to learn from, and 93 things I track about your world updated.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q\n\nfull:\n%s", want, got)
		}
	}
	for _, banned := range []string{"decayed", "hot-reset", "upserted", "entit(ies)", "session(s)", "—"} {
		if strings.Contains(got, banned) {
			t.Errorf("summary contains banned jargon %q\n\nfull:\n%s", banned, got)
		}
	}
}

// A fully quiet night reads as one calm line, no empty sections.
func TestReportSummary_QuietNight(t *testing.T) {
	got := Report{}.Summary()
	want := "A quiet night. I checked my memory and there was nothing new to learn."
	if got != want {
		t.Errorf("quiet night = %q, want %q", got, want)
	}
}

// A tidy-only night (no lessons) still explains itself without jargon.
func TestReportSummary_TidyOnly(t *testing.T) {
	r := Report{Consolidate: memory.ConsolidateReport{Decayed: 3}}
	got := r.Summary()
	if !strings.HasPrefix(got, "Nothing new to learn last night, but I gave my memory a good tidy-up.") {
		t.Errorf("unexpected lead: %q", got)
	}
	if !strings.Contains(got, "Tidy-up: 3 stale memories left to fade.") {
		t.Errorf("missing tidy clause: %q", got)
	}
}

// Stage errors surface as a Trouble section in plain English.
func TestReportSummary_Trouble(t *testing.T) {
	r := Report{
		Highlights: Highlights{Lessons: []string{"a lesson"}},
		Errors:     []StageError{{Stage: "reflect", Message: "context deadline exceeded"}},
	}
	got := r.Summary()
	if !strings.Contains(got, "I hit some trouble along the way; details below.") {
		t.Errorf("lead missing trouble note: %q", got)
	}
	if !strings.Contains(got, "Trouble: I could not finish everything. These steps hit problems: reflect: context deadline exceeded. I will try them again tomorrow night.") {
		t.Errorf("missing trouble section: %q", got)
	}
}

// An LLM-drafted digest replaces only the lead; sections stay deterministic.
func TestReportSummary_DigestLead(t *testing.T) {
	r := Report{
		Digest:     "Mostly a night of cleaning up after the failed self-improve run.",
		Highlights: Highlights{Lessons: []string{"a lesson"}},
	}
	got := r.Summary()
	if !strings.HasPrefix(got, "Mostly a night of cleaning up after the failed self-improve run.\n\n") {
		t.Errorf("digest lead not used: %q", got)
	}
	if !strings.Contains(got, "What I learned:\n- a lesson") {
		t.Errorf("sections missing under digest lead: %q", got)
	}
}

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
