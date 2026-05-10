package memory

import "testing"

func TestRRFFusionFavorsItemsAcrossStreams(t *testing.T) {
	// Item "A" appears in BM25 rank 1 and vector rank 1 → highest score.
	// Item "B" appears only in BM25 rank 1 → lower score.
	streams := [][]ScoredItem{
		{{ID: "A", Rank: 1}, {ID: "B", Rank: 2}},
		{{ID: "A", Rank: 1}, {ID: "C", Rank: 2}},
	}
	got := RRF(streams, 60)
	if len(got) != 3 {
		t.Fatalf("want 3 results, got %d", len(got))
	}
	if got[0].ID != "A" {
		t.Fatalf("want A first (appears in both streams), got %s", got[0].ID)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("expected A's score > #2's score, got %f vs %f", got[0].Score, got[1].Score)
	}
}

func TestRRFEmptyStreams(t *testing.T) {
	if got := RRF(nil, 60); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestDiversifyBySession(t *testing.T) {
	in := []SearchResult{
		{ObservationID: "1", SessionID: "s1"},
		{ObservationID: "2", SessionID: "s1"},
		{ObservationID: "3", SessionID: "s1"},
		{ObservationID: "4", SessionID: "s1"}, // dropped (4th from s1 with cap=3)
		{ObservationID: "5", SessionID: "s2"},
	}
	got := DiversifyBySession(in, 3)
	if len(got) != 4 {
		t.Fatalf("want 4 results, got %d", len(got))
	}
	if got[3].ObservationID != "5" {
		t.Fatalf("want s2 result preserved, got %s", got[3].ObservationID)
	}
}

func TestStripSecretsRedactsAnthropicKey(t *testing.T) {
	in := "my key is sk-ant-foobar1234567890abcdef and more"
	out, redacted := StripSecrets(in)
	if !redacted {
		t.Fatal("expected redaction to occur")
	}
	if got := out; got == in {
		t.Fatalf("expected redacted text, got %q", got)
	}
}

func TestStripSecretsHandlesPrivateTags(t *testing.T) {
	in := "ok <private>shhh secret</private> done"
	out, redacted := StripSecrets(in)
	if !redacted {
		t.Fatal("expected redaction to occur")
	}
	if got := out; got != "ok [PRIVATE CONTENT REMOVED] done" {
		t.Fatalf("unexpected output: %q", got)
	}
}
