package surface

import (
	"strings"
	"testing"
)

// The capture mechanic decides which rows are messages. If this drifts from the
// dashboard's read filter, either bodies stop being captured or non-messages
// start getting fetched.
func TestIsMessageItem(t *testing.T) {
	cases := []struct {
		surface, kind string
		want          bool
	}{
		{"followups", "email", true},
		{"inbox", "email", true},
		{"email", "email", true},
		{"Followups", "Email", true}, // case-insensitive: producers vary
		{"followups", "task", false},
		{"system", "email", false},
		{"runs", "run", false},
	}
	for _, c := range cases {
		if got := IsMessageItem(c.surface, c.kind); got != c.want {
			t.Errorf("IsMessageItem(%q,%q) = %v, want %v", c.surface, c.kind, got, c.want)
		}
	}
}

// The list row was reading "sender / subject / sender" because the only preview
// available was the subtitle. The snippet has to come out of the real message,
// readable, with markup and boilerplate gone.
func TestSnippet(t *testing.T) {
	html := `<html><head><style>.x{color:red}</style></head><body><p>Hey Kai,</p>
	<p>Following up on the&nbsp;proposal &amp; timeline.</p><script>track()</script></body></html>`
	got := Snippet("", html, 240)
	want := "Hey Kai, Following up on the proposal & timeline."
	if got != want {
		t.Errorf("Snippet(html) = %q, want %q", got, want)
	}

	// Plain text wins when present: it's what the sender actually typed.
	if got := Snippet("  plain   body  ", html, 240); got != "plain body" {
		t.Errorf("text should win over html, got %q", got)
	}
	if got := Snippet("", "", 240); got != "" {
		t.Errorf("empty in, empty out; got %q", got)
	}
	// Long bodies cut on a word boundary, not mid-word.
	long := "alpha bravo charlie delta echo foxtrot golf hotel india juliet"
	got = Snippet(long, "", 20)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated snippet must end in an ellipsis, got %q", got)
	}
	if len([]rune(got)) > 21 {
		t.Errorf("snippet exceeded the requested length, got %q (%d runes)", got, len([]rune(got)))
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Errorf("snippet should not end on a dangling space, got %q", got)
	}
	if got[:5] != "alpha" {
		t.Errorf("truncation must keep the start of the message, got %q", got)
	}
}

// A capture that produced a body must fill the fields the boss SEES, and must
// never overwrite something a producer deliberately wrote.
func TestApplyBodyDerived(t *testing.T) {
	it := &Item{}
	applyBodyDerived(it, "The quote came in at 4200.", "")
	if it.Metadata["preview"] != "The quote came in at 4200." {
		t.Errorf("preview not derived: %#v", it.Metadata)
	}
	if it.Body == "" {
		t.Error("an empty Context pane should be filled from the message")
	}

	authored := &Item{Body: "Triage: he needs a yes/no on the quote.", Metadata: map[string]any{"preview": "custom"}}
	applyBodyDerived(authored, "The quote came in at 4200.", "")
	if authored.Body != "Triage: he needs a yes/no on the quote." {
		t.Error("a producer-authored summary must survive capture")
	}
	if authored.Metadata["preview"] != "custom" {
		t.Error("a producer-authored preview must survive capture")
	}
}

// The case the backfill sweep originally skipped: a row that already has a
// Context summary still needs its list preview derived. Deriving must be judged
// per FIELD, never "did anything on the item change".
func TestApplyBodyDerived_FillsPreviewWhenBodyAlreadySet(t *testing.T) {
	it := &Item{Body: "Triage: he wants a decision on the quote."}
	applyBodyDerived(it, "The quote came in at 4200, can you confirm?", "")
	if it.Metadata["preview"] == "" || it.Metadata["preview"] == nil {
		t.Fatal("a row with a summary but no preview must still get one")
	}
	if it.Body != "Triage: he wants a decision on the quote." {
		t.Error("the existing summary must not be replaced")
	}
}

// A failed capture has to leave a trail: the sweep reads the attempt count to
// stop retrying a dead account, and the reason has to stay readable on the row.
func TestCaptureFailureStamps(t *testing.T) {
	s := &Store{}
	it := &Item{}
	s.stampCaptureFailure(it, "upstream unsuccessful: account revoked")
	if it.Metadata["body_fetch_attempts"] != 1 {
		t.Errorf("first failure should record attempt 1, got %#v", it.Metadata["body_fetch_attempts"])
	}
	s.stampCaptureFailure(it, "upstream unsuccessful: account revoked")
	if it.Metadata["body_fetch_attempts"] != 2 {
		t.Errorf("second failure should record attempt 2, got %#v", it.Metadata["body_fetch_attempts"])
	}
	if it.Metadata["body_fetch_error"] == "" {
		t.Error("the reason must be kept on the row")
	}
	clearCaptureFailure(it)
	if _, still := it.Metadata["body_fetch_error"]; still {
		t.Error("a later success must clear the failure stamp")
	}
}
