package tools

import "testing"

func TestSessionForPublishUsesParentSession(t *testing.T) {
	RegisterRunForSession("background:child", "run-1", "chat-parent")
	defer UnregisterRunForSession("background:child")

	if got := SessionForPublish("background:child"); got != "chat-parent" {
		t.Fatalf("SessionForPublish(child) = %q, want parent", got)
	}
	if got := SessionForPublish("chat-parent"); got != "chat-parent" {
		t.Fatalf("SessionForPublish(parent) = %q, want same", got)
	}
	if got := SessionForPublish(""); got != "" {
		t.Fatalf("SessionForPublish(empty) = %q, want empty", got)
	}
}