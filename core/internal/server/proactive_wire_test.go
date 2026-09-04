package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

func newRegistryServer() *Server {
	return &Server{activeSessions: make(map[string]map[string]func(wsServerEvent))}
}

func TestSessionSenderPublishesToParentSession(t *testing.T) {
	srv := newRegistryServer()
	tools.RegisterRunForSession("background:child", "run-1", "chat-parent")
	defer tools.UnregisterRunForSession("background:child")

	got := make(chan wsServerEvent, 1)
	srv.registerSession("chat-parent", "c1", func(ev wsServerEvent) { got <- ev })

	srv.sessionSender("background:child")(wsServerEvent{Type: "tool_call", SessionID: "background:child"})
	select {
	case ev := <-got:
		if ev.SessionID != "chat-parent" {
			t.Fatalf("session sender published to %q, want chat-parent", ev.SessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for parent session event")
	}
}

func TestBroadcastBackgroundDoneTargetsOriginatingSession(t *testing.T) {
	srv := newRegistryServer()
	got := make(chan wsServerEvent, 1)
	srv.registerSession("chat-parent", "c1", func(ev wsServerEvent) { got <- ev })

	srv.BroadcastBackgroundDone("chat-parent", "Fix it", "All good", "")
	ev := <-got
	if ev.SessionID != "chat-parent" {
		t.Fatalf("done event session = %q, want chat-parent", ev.SessionID)
	}
	if ev.FindingKind != "background_build" {
		t.Fatalf("finding kind = %q", ev.FindingKind)
	}
}

// ── one session, many sockets (2026-09-04) ─────────────────────────────────
//
// The registry used to hold ONE send per session, last registration wins. Two
// real costs: opening the same chat on the phone blinded the laptop, and the
// "same-pointer guard" on unregister compared func values with %p, which is
// the code pointer and identical for every closure, so a slow-closing old
// socket evicted the fresh one that had just replaced it. Both pinned here.

func TestSessionSender_FansOutToEveryTab(t *testing.T) {
	srv := newRegistryServer()
	laptop := make(chan wsServerEvent, 1)
	phone := make(chan wsServerEvent, 1)
	srv.registerSession("chat", "laptop", func(ev wsServerEvent) { laptop <- ev })
	srv.registerSession("chat", "phone", func(ev wsServerEvent) { phone <- ev })

	srv.sessionSender("chat")(wsServerEvent{Type: "delta", Text: "hi"})
	for name, ch := range map[string]chan wsServerEvent{"laptop": laptop, "phone": phone} {
		select {
		case ev := <-ch:
			if ev.Text != "hi" {
				t.Fatalf("%s got %+v", name, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s never received the frame: a second tab must not blind the first", name)
		}
	}
}

func TestUnregister_ByConnIDNeverEvictsTheOtherTab(t *testing.T) {
	srv := newRegistryServer()
	fresh := make(chan wsServerEvent, 1)
	srv.registerSession("chat", "old", func(ev wsServerEvent) { t.Fatal("the closed socket must not receive anything") })
	srv.registerSession("chat", "fresh", func(ev wsServerEvent) { fresh <- ev })

	// The old socket's deferred cleanup runs AFTER the fresh one registered -
	// the reconnect race. Only its own binding may go.
	srv.unregisterSession("chat", "old")
	srv.sessionSender("chat")(wsServerEvent{Type: "complete"})
	select {
	case <-fresh:
	case <-time.After(time.Second):
		t.Fatal("the fresh socket lost its binding to a stale unregister - the reconnect race is not guarded")
	}
	// And the last binding going removes the session entirely.
	srv.unregisterSession("chat", "fresh")
	if len(srv.sessionSenders("chat")) != 0 {
		t.Fatal("an unbound session must have no senders left")
	}
}

// ── classifier recent context (2026-08-28) ─────────────────────────────────
//
// Both classifiers were called with a hard-coded empty recentContext, while
// their own contract says it should carry "the last few user/assistant turns".
// With nothing on record as proposed or underway, "please continue the build
// and finish up" could not read as an approval, so it classified as `discuss`
// and the consent gate refused plan_update for the rest of the turn.

func TestBuildRecentContextCarriesTheExchangeAndThePlan(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "start on the build-to-done work"},
		{Role: llm.RoleAssistant, Content: "On it.\nStep 1 is\tunderway"},
		{Role: llm.RoleUser, Content: "please continue the build and finish up"},
	}
	note := `Already underway in this conversation: the plan "Make Jarvis finish long coding tasks", which the boss approved — status paused, 1 of 4 steps finished.`

	got := buildRecentContext(msgs, note)
	if got == "" {
		t.Fatal("the classifier must not be called blind: the context block is empty")
	}
	for _, want := range []string{"Boss: start on the build-to-done work", "Jarvis: On it. Step 1 is underway", "please continue the build", "which the boss approved", "paused"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n\nJarvis") || strings.Count(got, "\n\n") != 1 {
		t.Fatalf("turns run together, one blank line before the plan note:\n%s", got)
	}
}

func TestBuildRecentContextStaysBounded(t *testing.T) {
	var msgs []llm.Message
	for i := 0; i < 40; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 5000)})
		msgs = append(msgs, llm.Message{Role: llm.RoleTool, Content: "tool noise that must not ride along"})
	}
	kept := tailConversation(msgs, recentContextTurns)
	if len(kept) != recentContextTurns {
		t.Fatalf("kept %d turns, want the last %d", len(kept), recentContextTurns)
	}
	got := buildRecentContext(kept, "")
	if strings.Contains(got, "tool noise") {
		t.Fatal("tool traffic must not ride along: it is noise for an intent read and most of the tokens")
	}
	if lines := strings.Split(got, "\n"); len(lines) != recentContextTurns {
		t.Fatalf("got %d lines, want %d", len(lines), recentContextTurns)
	}
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > recentContextChars+len("Boss: ")+1 {
			t.Fatalf("a turn was not clipped (%d runes)", len([]rune(line)))
		}
	}
}

func TestBuildRecentContextEmptyWhenThereIsNothingToSay(t *testing.T) {
	if got := buildRecentContext(nil, ""); got != "" {
		t.Fatalf("with no turns and no plan the block must stay empty, got %q", got)
	}
}

// TestRecentContextForReadsTheLiveConversation proves the wiring, not just the
// shape: the builder the classifiers are handed returns the real session's
// trailing turns, and it does so WITHOUT minting a session.
func TestRecentContextForReadsTheLiveConversation(t *testing.T) {
	loop := agent.New(agent.Config{})
	sess := loop.GetOrCreateSession("11111111-2222-3333-4444-555555555555")
	sess.Append(llm.Message{Role: llm.RoleUser, Content: "start on the build-to-done work"})
	sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "On it."})
	sess.Append(llm.Message{Role: llm.RoleUser, Content: "please continue the build and finish up"})

	srv := &Server{loop: loop}
	got := recentContext(context.Background(), srv.recentContextFn(sess.ID))
	if got == "" {
		t.Fatal("the classifier is still being called blind on a live conversation")
	}
	if !strings.Contains(got, "please continue the build and finish up") {
		t.Fatalf("the latest exchange is missing:\n%s", got)
	}

	// An unknown session must not be created as a side effect of classifying.
	before := len(loop.Sessions())
	if other := srv.recentContextFor(context.Background(), "99999999-2222-3333-4444-555555555555"); other != "" {
		t.Fatalf("unknown session should yield no context, got %q", other)
	}
	if after := len(loop.Sessions()); after != before {
		t.Fatalf("building the classifier's context minted a session (%d -> %d)", before, after)
	}
}

func TestRecentContextIsNilSafe(t *testing.T) {
	var srv *Server
	if got := srv.recentContextFor(context.Background(), "sid"); got != "" {
		t.Fatalf("nil server must yield no context, got %q", got)
	}
	empty := &Server{}
	if got := recentContext(context.Background(), empty.recentContextFn("")); got != "" {
		t.Fatalf("no loop and no pool must yield no context, got %q", got)
	}
	if got := recentContext(context.Background(), nil); got != "" {
		t.Fatalf("a nil builder must yield no context, got %q", got)
	}
}
