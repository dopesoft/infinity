package voyager

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Mining tests are pure-function over the cluster algorithm so they run
// without a database. Live DB persistence is covered separately by the
// nightly maintenance integration tests once a postgres fixture is available.

func TestPromptKeywords_FiltersStopwords(t *testing.T) {
	keys := promptKeywords("Please summarize my unread email from this morning")
	for _, k := range keys {
		switch k {
		case "please", "from", "this", "your":
			t.Fatalf("stopword %q leaked into keywords %v", k, keys)
		}
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one keyword from a substantive prompt")
	}
}

func TestPromptKeywords_DropsShortTokensAndPunctuation(t *testing.T) {
	keys := promptKeywords("Pull up tomorrow's calendar, please!!!")
	for _, k := range keys {
		if len(k) < 4 {
			t.Fatalf("token %q kept despite being below the length floor", k)
		}
		for _, r := range k {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				t.Fatalf("non-alphanumeric leaked into keyword %q", k)
			}
		}
	}
}

func TestSignatureIsOrderInvariant(t *testing.T) {
	a := signatureFromKeywords(promptKeywords("draft a tweet about the new release"))
	b := signatureFromKeywords(promptKeywords("about the new release, draft a tweet"))
	if a != b {
		t.Fatalf("signatures should be order-invariant: %q vs %q", a, b)
	}
}

func TestClusterPrompts_GroupsByFingerprint(t *testing.T) {
	now := time.Now().UTC()
	prompts := []promptRow{
		{ID: "o1", SessionID: "s1", Text: "summarize my unread email this morning", At: now.Add(-72 * time.Hour)},
		{ID: "o2", SessionID: "s2", Text: "could you summarize my unread email please", At: now.Add(-48 * time.Hour)},
		{ID: "o3", SessionID: "s3", Text: "summarize unread email for me", At: now.Add(-1 * time.Hour)},
		// noise: tiny ack
		{ID: "o4", SessionID: "s2", Text: "ok", At: now},
		// distinct cluster
		{ID: "o5", SessionID: "s4", Text: "render the slow burn short ASAP", At: now},
	}
	clusters := ClusterPrompts(prompts)
	if len(clusters) == 0 {
		t.Fatal("expected at least one cluster")
	}
	// The summarize-email cluster should be top.
	top := clusters[0]
	if top.Hits < 3 {
		t.Fatalf("top cluster hits=%d, want >=3", top.Hits)
	}
	if top.DistinctSessions < 3 {
		t.Fatalf("top cluster distinct sessions=%d, want >=3", top.DistinctSessions)
	}
	foundEmail := false
	for _, k := range top.Keywords {
		if k == "email" {
			foundEmail = true
		}
	}
	if !foundEmail {
		t.Fatalf("expected 'email' in top cluster keywords, got %v", top.Keywords)
	}
}

func TestClusterMeetsThreshold(t *testing.T) {
	c := Cluster{Hits: 3, DistinctSessions: 2, DistinctDays: 2}
	if !c.meetsThreshold() {
		t.Fatal("cluster at exact threshold should pass")
	}
	c = Cluster{Hits: 2, DistinctSessions: 2, DistinctDays: 2}
	if c.meetsThreshold() {
		t.Fatal("cluster below min hits should fail")
	}
	c = Cluster{Hits: 5, DistinctSessions: 1, DistinctDays: 5}
	if c.meetsThreshold() {
		t.Fatal("cluster from single session should fail breadth check")
	}
	c = Cluster{Hits: 5, DistinctSessions: 3, DistinctDays: 1}
	if c.meetsThreshold() {
		t.Fatal("cluster on single day should fail breadth check")
	}
}

func TestConfidenceMonotonicInHits(t *testing.T) {
	now := time.Now().UTC()
	small := Cluster{Hits: 3, DistinctDays: 2, LastAt: now}
	big := Cluster{Hits: 20, DistinctDays: 14, LastAt: now}
	if big.Confidence() <= small.Confidence() {
		t.Fatalf("confidence should rise with hits + breadth, got small=%d big=%d", small.Confidence(), big.Confidence())
	}
	if big.Confidence() > 100 {
		t.Fatalf("confidence capped at 100, got %d", big.Confidence())
	}
}

func TestConfidenceUsesRecency(t *testing.T) {
	now := time.Now().UTC()
	fresh := Cluster{Hits: 5, DistinctDays: 5, LastAt: now}
	stale := Cluster{Hits: 5, DistinctDays: 5, LastAt: now.Add(-29 * 24 * time.Hour)}
	if fresh.Confidence() <= stale.Confidence() {
		t.Fatalf("fresher cluster should outrank stale one, got fresh=%d stale=%d", fresh.Confidence(), stale.Confidence())
	}
}

func TestExternalIDIsStable(t *testing.T) {
	cl := Cluster{Signature: "calendar tomorrow"}
	a := cl.externalID()
	b := cl.externalID()
	if a != b {
		t.Fatalf("external id should be deterministic, got %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "routine:") {
		t.Fatalf("external id should be namespaced under routine:, got %q", a)
	}
}

func TestDraftSkillMDIncludesFrontmatterAndTriggers(t *testing.T) {
	cl := Cluster{
		Keywords: []string{"summarize", "unread", "email"},
		Examples: []string{
			"summarize my unread email this morning",
			"summarize unread email please",
		},
	}
	md := cl.draftSkillMD()
	if !strings.HasPrefix(md, "---") {
		t.Fatal("SKILL.md must start with frontmatter")
	}
	if !strings.Contains(md, "name: routine_") {
		t.Fatalf("missing name field: %s", md)
	}
	if !strings.Contains(md, "trigger_phrases:") {
		t.Fatalf("missing trigger phrases: %s", md)
	}
	if !strings.Contains(md, "summarize my unread email") {
		t.Fatalf("expected first example as a trigger phrase: %s", md)
	}
	if !strings.Contains(md, "source: routine-miner") {
		t.Fatalf("expected source provenance in frontmatter: %s", md)
	}
}

func TestProvenanceMarkdownIncludesSessionsAndObservations(t *testing.T) {
	cl := Cluster{
		Signature:        "email summarize unread",
		Keywords:         []string{"email", "summarize", "unread"},
		Hits:             4,
		DistinctSessions: 3,
		DistinctDays:     3,
		LastAt:           time.Now().UTC(),
		SessionIDs:       []string{"sess-1", "sess-2"},
		ObservationIDs:   []string{"obs-1", "obs-2", "obs-3"},
		Examples:         []string{"summarize my unread email"},
	}
	prov := cl.provenanceMarkdown()
	for _, want := range []string{"sess-1", "obs-1", "summarize my unread email", "Cluster signature"} {
		if !strings.Contains(prov, want) {
			t.Fatalf("expected provenance to mention %q, got:\n%s", want, prov)
		}
	}
}

func TestChatBubbleHasNoEmDashAndIncludesProposalID(t *testing.T) {
	cl := Cluster{
		Hits:             5,
		DistinctSessions: 3,
		DistinctDays:     3,
		Examples:         []string{"summarize my unread email"},
	}
	body := cl.chatBubble("abc-123")
	if strings.Contains(body, "—") || strings.Contains(body, "–") {
		t.Fatalf("chat bubble must not contain em or en dashes: %q", body)
	}
	if !strings.Contains(body, "abc-123") {
		t.Fatal("chat bubble should cite the proposal id so the boss can find it")
	}
	if !strings.Contains(body, "Noticed a routine") {
		t.Fatalf("chat bubble missing routine framing: %q", body)
	}
}

func TestNotifierIsCalledOnceWithinWindow(t *testing.T) {
	m := NewRoutineMiner(nil, nil, nil)
	var calls int
	var mu sync.Mutex
	m.SetNotifier(func(_, _, _ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	cl := Cluster{Signature: "sig-x", Hits: 3, DistinctSessions: 2, DistinctDays: 2, Examples: []string{"hi"}}
	m.maybeNotifyChat("sess-A", cl, "prop-1")
	m.maybeNotifyChat("sess-A", cl, "prop-1")
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 notify within the dedup window, got %d", calls)
	}
}

func TestNotifierFiresPerSession(t *testing.T) {
	m := NewRoutineMiner(nil, nil, nil)
	var calls int
	var mu sync.Mutex
	m.SetNotifier(func(_, _, _ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	cl := Cluster{Signature: "sig-y", Hits: 3, DistinctSessions: 2, DistinctDays: 2, Examples: []string{"hi"}}
	m.maybeNotifyChat("sess-A", cl, "prop-1")
	m.maybeNotifyChat("sess-B", cl, "prop-1")
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected each distinct session to receive its own bubble, got %d", calls)
	}
}

func TestRoutineMinerNilSafe(t *testing.T) {
	var m *RoutineMiner
	m.RunNightly(nil, nil) // must not panic
	m.SetNotifier(nil)
	if _, err := m.MineAndPropose(nil, time.Now(), ""); err != nil {
		t.Fatalf("nil miner should degrade quietly, got err %v", err)
	}
}

func TestProposedNameIsSnakeCased(t *testing.T) {
	cl := Cluster{Keywords: []string{"email", "summarize"}}
	got := cl.proposedName()
	if !strings.HasPrefix(got, "routine_") {
		t.Fatalf("expected routine_ prefix, got %q", got)
	}
	if strings.ContainsAny(got, " -") {
		t.Fatalf("name should be snake_case, got %q", got)
	}
}
