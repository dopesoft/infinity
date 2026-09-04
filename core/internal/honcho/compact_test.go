package honcho

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormaliseStem(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[2026-05-28 06:00:02] boss instructed that no messages should be sent directly.", "no messages should be sent directly"},
		{"[2026-05-31 00:02:15] Boss instructed to cap   max_messages at 20", "to cap max_messages at 20"},
		{"[2026-06-01 09:00:00] The boss said that drafts only should be created!", "drafts only should be created"},
		{"[2026-06-02 09:00:00] boss asked drafts only should be created", "drafts only should be created"},
		{"[id:abc-123] [2026-06-02 09:00:00] boss set the timezone to CST", "the timezone to cst"},
		{"3. [2026-06-02 09:00:00] boss wants coffee", "coffee"},
		{"- boss mentioned that he prefers plain english", "he prefers plain english"},
		{"   ", ""},
		// No lead-in: the sentence is the stem.
		{"[2026-06-02 09:00:00] Prefers short, casual times; never UTC.", "prefers short, casual times; never utc"},
	}
	for _, c := range cases {
		if got := normaliseStem(c.in); got != c.want {
			t.Errorf("normaliseStem(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCompactRepresentationDedupesKeepingNewest(t *testing.T) {
	rep := strings.Join([]string{
		"## Explicit Observations",
		"[2026-05-28 06:00:02] boss instructed that no messages should be sent directly and that drafts only should be created",
		"[2026-05-29 06:00:01] boss instructed that no messages should be sent directly and that drafts only should be created.",
		"[2026-05-31 00:02:15] boss instructed to cap max_messages at 20",
		"[2026-06-01 06:00:03] Boss said that no messages should be sent directly and that drafts only should be created",
		"[2026-05-30 00:02:15] boss instructed to cap max_messages at 20",
		"",
		"## Deductive Observations",
		"[2026-05-20 10:00:00] boss prefers drafts over sends",
		"   Premises:",
		"   - he said drafts only",
	}, "\n")
	got := CompactRepresentation(rep)

	if n := strings.Count(got, "drafts only should be created"); n != 1 {
		t.Fatalf("expected the repeated instruction once, got %d in:\n%s", n, got)
	}
	if !strings.Contains(got, "[2026-06-01 06:00:03]") {
		t.Fatalf("newest instance should survive:\n%s", got)
	}
	if strings.Contains(got, "[2026-05-28 06:00:02]") || strings.Contains(got, "[2026-05-29 06:00:01]") {
		t.Fatalf("older instances should be dropped:\n%s", got)
	}
	if n := strings.Count(got, "cap max_messages at 20"); n != 1 || !strings.Contains(got, "[2026-05-31 00:02:15]") {
		t.Fatalf("second stem should keep only its newest row:\n%s", got)
	}
	// Newest first inside the section.
	if strings.Index(got, "[2026-06-01") > strings.Index(got, "[2026-05-31") {
		t.Fatalf("expected newest-first ordering:\n%s", got)
	}
	// Premises ride along with their observation; headings survive.
	for _, want := range []string{"## Explicit Observations", "## Deductive Observations", "   Premises:", "   - he said drafts only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "more, ask recall") {
		t.Fatalf("nothing should be truncated under the cap:\n%s", got)
	}
}

func TestCompactRepresentationCapsWithTrailer(t *testing.T) {
	var lines []string
	lines = append(lines, "## Explicit Observations")
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("[2026-06-%02d 09:00:00] boss said that distinct fact number %d is worth remembering for a long time", i%28+1, i))
	}
	rep := strings.Join(lines, "\n")
	got := compactRepresentation(rep, 1000)
	if len(got) > 1000 {
		t.Fatalf("cap breached: %d chars", len(got))
	}
	if !strings.Contains(got, "more, ask recall for detail)") {
		t.Fatalf("expected truncation trailer:\n%s", got)
	}
	kept := strings.Count(got, "[2026-06-")
	var dropped int
	if _, err := fmt.Sscanf(got[strings.LastIndex(got, "(+"):], "(+%d more", &dropped); err != nil {
		t.Fatalf("trailer unparsable: %v\n%s", err, got)
	}
	if kept+dropped != 60 {
		t.Fatalf("kept %d + dropped %d != 60", kept, dropped)
	}
	// Newest first: the first observation rendered is the latest date.
	first := got[strings.Index(got, "[2026-06-"):]
	if !strings.HasPrefix(first, "[2026-06-28") {
		t.Fatalf("expected newest observation first, got %q", first[:22])
	}
}

func TestCompactRepresentationEmpty(t *testing.T) {
	if got := CompactRepresentation("  \n\n"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
