package proactive

import "testing"

// Drafts are non-destructive (saved, never sent), so composing/editing a draft
// must NEVER be gated — the boss's explicit rule: "I don't need to approve an
// email draft, just write it." Only SENDING leaves the system and stays gated.
func TestComposioGate_DraftsNotGatedButSendsAre(t *testing.T) {
	g := NewComposioGate(nil)
	cases := []struct {
		suffix   string
		wantGate bool
		why      string
	}{
		{"GMAIL_CREATE_EMAIL_DRAFT", false, "drafting is non-destructive"},
		{"GMAIL_CREATE_DRAFT_REPLY", false, "draft reply is still just a draft"},
		{"GMAIL_UPDATE_DRAFT", false, "editing a draft never leaves the system"},
		{"GMAIL_SEND_EMAIL", true, "sending leaves the system"},
		{"GMAIL_SEND_DRAFT", true, "sending a draft still sends"},
		{"GMAIL_LIST_MESSAGES", false, "reads are never gated"},
		{"GMAIL_FETCH_EMAILS", false, "reads are never gated"},
		{"SLACK_SEND_MESSAGE", true, "outbound message is gated"},
		{"GITHUB_CREATE_ISSUE", true, "a real CREATE write stays gated"},
	}
	for _, c := range cases {
		got, _ := g.shouldGate(c.suffix)
		if got != c.wantGate {
			t.Errorf("shouldGate(%q) = %v, want %v (%s)", c.suffix, got, c.wantGate, c.why)
		}
	}
}
