package server

import (
	"testing"
	"time"
)

// The last thing Jarvis said in a turn is his reply, never narration.
//
// 2026-09-04: a 19,000-character research report was filed interim (a tool
// call followed it), the ChatGPT plan ran dry four seconds later, the turn
// closed errored with no reply row, and Studio folded the report into a
// collapsed "Talked it through 2 times" line. These pin the rule that
// un-folds it, and every case where the rule must NOT fire.

func idx(n int) *int { return &n }

func assistant(turn string, i int, interim bool) sessionMessageDTO {
	return sessionMessageDTO{TurnID: turn, Role: "assistant", Text: "t", Interim: interim, MessageIndex: idx(i)}
}

func TestPromote_AnErroredTurnsLastMessageIsTheReply(t *testing.T) {
	rows := []sessionMessageDTO{
		{TurnID: "t1", Role: "user", Text: "go"},
		assistant("t1", 0, true),
		{TurnID: "t1", Role: "tool", ToolCallID: "c1"},
		assistant("t1", 1, true),
		assistant("t1", 2, true),
		{TurnID: "t1", Role: "tool", ToolCallID: "c2"},
	}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {}}, time.Now())
	if out[1].Interim != true || out[3].Interim != true {
		t.Fatalf("earlier narration must stay folded: %+v", out)
	}
	if out[4].Interim {
		t.Fatalf("the last message (index 2) must be un-folded: %+v", out[4])
	}
}

func TestPromote_LastIsByMessageIndexNotPosition(t *testing.T) {
	// Two async inserts landed out of order: index 2 sits before index 1.
	rows := []sessionMessageDTO{assistant("t1", 2, true), assistant("t1", 1, true)}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {}}, time.Now())
	if out[0].Interim || !out[1].Interim {
		t.Fatalf("index 2 is the later message wherever it sits: %+v", out)
	}
}

func TestPromote_ATurnWithItsOwnReplyIsUntouched(t *testing.T) {
	rows := []sessionMessageDTO{assistant("t1", 0, true), {TurnID: "t1", Role: "assistant", Text: "done", MessageIndex: idx(1)}}
	out := promoteLastReply(rows, map[string]bool{"t1": true}, map[string]turnMeta{"t1": {hasText: true}}, time.Now())
	if !out[0].Interim {
		t.Fatalf("narration before a real reply stays folded: %+v", out)
	}
}

func TestPromote_ALiveTurnIsUntouched(t *testing.T) {
	rows := []sessionMessageDTO{assistant("t1", 0, true)}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {live: true}}, time.Now())
	if !out[0].Interim {
		t.Fatal("a turn still running has not said its last word yet")
	}
}

func TestPromote_WaitsForAReplyRowThatIsStillLanding(t *testing.T) {
	// closeTurn wrote assistant_text a second ago; the TaskCompleted row is on
	// its way. Un-folding the interim now would show two replies for a moment.
	rows := []sessionMessageDTO{assistant("t1", 0, true)}
	fresh := map[string]turnMeta{"t1": {hasText: true, endedAt: time.Now().Add(-time.Second)}}
	if out := promoteLastReply(rows, nil, fresh, time.Now()); !out[0].Interim {
		t.Fatal("inside the grace window the interim must stay folded")
	}
	stale := map[string]turnMeta{"t1": {hasText: true, endedAt: time.Now().Add(-time.Minute)}}
	if out := promoteLastReply(rows, nil, stale, time.Now()); out[0].Interim {
		t.Fatal("past the grace window a turn with no reply row promotes its last message")
	}
}

func TestPromote_IterationCapTurnPromotes(t *testing.T) {
	// No reply row, no assistant_text: the last interim is all he got.
	rows := []sessionMessageDTO{assistant("t1", 0, true), assistant("t1", 1, true)}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {endedAt: time.Now()}}, time.Now())
	if out[1].Interim {
		t.Fatal("an iteration-cap turn's last message is its reply")
	}
}

func TestPromote_RowsWithoutATurnAndErrorCardsAreNeverPromoted(t *testing.T) {
	rows := []sessionMessageDTO{
		{Role: "assistant", Text: "old", Interim: true},
		{TurnID: "t1", Role: "assistant", Kind: "error", Text: "boom", Interim: true},
	}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {}}, time.Now())
	if !out[0].Interim || !out[1].Interim {
		t.Fatalf("nothing to promote here: %+v", out)
	}
}

func TestPromote_TheErrorCardFollowsTheReplyItExplains(t *testing.T) {
	// closeTurn stamps the error before the async reply row lands, so the
	// error sorted first. Live order was reply, then error.
	rows := []sessionMessageDTO{
		{TurnID: "t1", Role: "user", Text: "go"},
		{TurnID: "t1", Role: "assistant", Kind: "error", Text: "usage limit"},
		assistant("t1", 0, true),
		{TurnID: "t2", Role: "user", Text: "next"},
	}
	out := promoteLastReply(rows, nil, map[string]turnMeta{"t1": {}, "t2": {live: true}}, time.Now())
	if len(out) != 4 || out[1].Kind != "" || out[1].Interim || out[2].Kind != "error" || out[3].TurnID != "t2" {
		t.Fatalf("expected user, reply, error, next; got %+v", out)
	}
}
