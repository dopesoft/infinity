package server

import (
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

func TestSessionSenderPublishesToParentSession(t *testing.T) {
	srv := &Server{activeSessions: make(map[string]func(wsServerEvent))}
	tools.RegisterRunForSession("background:child", "run-1", "chat-parent")
	defer tools.UnregisterRunForSession("background:child")

	got := make(chan wsServerEvent, 1)
	srv.registerSession("chat-parent", func(ev wsServerEvent) { got <- ev })

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
	srv := &Server{activeSessions: make(map[string]func(wsServerEvent))}
	got := make(chan wsServerEvent, 1)
	srv.registerSession("chat-parent", func(ev wsServerEvent) { got <- ev })

	srv.BroadcastBackgroundDone("chat-parent", "Fix it", "All good", "")
	ev := <-got
	if ev.SessionID != "chat-parent" {
		t.Fatalf("done event session = %q, want chat-parent", ev.SessionID)
	}
	if ev.FindingKind != "background_build" {
		t.Fatalf("finding kind = %q", ev.FindingKind)
	}
}