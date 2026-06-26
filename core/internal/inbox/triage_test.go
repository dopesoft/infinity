package inbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/surface"
)

type fakeProvider struct {
	text string
}

func (f fakeProvider) Name() string  { return "fake" }
func (f fakeProvider) Model() string { return "fake-model" }

func (f fakeProvider) Stream(_ context.Context, _ string, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	if out != nil {
		out <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: f.text}
	}
	// Providers do not own or close the caller's stream channel.
	return llm.Response{Text: f.text}, nil
}

func TestClassifyDoesNotWaitForProviderToCloseStream(t *testing.T) {
	d := Deps{LLM: fakeProvider{text: `[{"index":0,"needs_reply":true,"reason":"Direct ask","importance":82}]`}}
	emails := []email{{from: "Alice <alice@example.com>", subject: "Can you review this?", snippet: "Need your input."}}

	done := make(chan map[int]decision, 1)
	go func() {
		done <- d.classify(context.Background(), emails)
	}()

	select {
	case got := <-done:
		if !got[0].NeedsReply {
			t.Fatalf("expected email 0 to need a reply, got %#v", got[0])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("classify hung waiting for the provider to close the stream")
	}
}

// The run report must read like Jarvis talking, naming actual senders and
// subjects — never "Triaged 12 email(s) across 4 mailbox(es)" dev-speak.
func TestSummaryHuman(t *testing.T) {
	cases := []struct {
		name string
		s    Summary
		want string
	}{
		{"no mail, one box", Summary{Accounts: 1}, "I checked your inbox and there was no new mail."},
		{"no mail, many boxes", Summary{Accounts: 4}, "I checked your 4 mailboxes and there was no new mail."},
		{"nothing needs reply", Summary{Accounts: 2, Fetched: 7},
			"I read 7 new emails in your 2 mailboxes. None of them need a reply from you."},
		{"flagged with examples", Summary{Accounts: 4, Fetched: 12, Surfaced: 3,
			Examples: []string{`Namecheap Support about "Your domain is expiring"`, `Stripe about "Payout failed"`, `X about "Y"`}},
			`I read 12 new emails in your 4 mailboxes and flagged 3 that need your reply, including Namecheap Support about "Your domain is expiring" and Stripe about "Payout failed". They are waiting in your Follow-ups.`},
		{"single flag grammar", Summary{Accounts: 1, Fetched: 1, Surfaced: 1, Examples: []string{`A about "B"`}},
			`I read 1 new email in your inbox and flagged 1 that needs your reply, including A about "B". They are waiting in your Follow-ups.`},
	}
	for _, c := range cases {
		if got := c.s.Human(); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestSenderName(t *testing.T) {
	if got := senderName(`"Namecheap Support" <support@namecheap.com>`); got != "Namecheap Support" {
		t.Errorf("senderName = %q", got)
	}
	if got := senderName("<bare@addr.com>"); got != "bare@addr.com" {
		t.Errorf("senderName bare = %q", got)
	}
}

func TestRunFailsLoudWhenNoMailboxReachable(t *testing.T) {
	sum, err := Run(context.Background(), Deps{
		Exec:    connectors.NewExecuteClient(func() string { return "project-key" }),
		Cache:   connectors.New(nil, func() string { return "project-key" }),
		Surface: surface.NewStore(nil, nil),
	}, Config{})

	if err == nil {
		t.Fatal("Run returned nil error, want blind-run failure")
	}
	if !strings.Contains(err.Error(), "could not reach any mailbox") ||
		!strings.Contains(err.Error(), "no active Gmail connection") {
		t.Fatalf("error = %q, want no-active-Gmail blind-run failure", err.Error())
	}
	if sum.Fetched != 0 || sum.Surfaced != 0 {
		t.Fatalf("summary = %#v, want no fetched/surfaced mail", sum)
	}
}
