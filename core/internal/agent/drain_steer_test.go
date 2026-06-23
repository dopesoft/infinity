package agent

import (
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// hookCapture is a minimal HookEmitter that records every Emit call.
type hookCapture struct {
	calls []string
}

func (h *hookCapture) Emit(name, sessionID, project, text string, payload map[string]any) {
	h.calls = append(h.calls, name)
}

// TestDrainSteer_AppendsToSessionWithoutFiringHook verifies the post-fix
// contract: drainSteer appends each drained message to the session's in-memory
// message list, but does NOT fire any hook. The UserPromptSubmit hook is now
// the WS handler's responsibility (fired immediately in steerTurn), so
// drainSteer firing it would create a duplicate observation.
func TestDrainSteer_AppendsToSessionWithoutFiringHook(t *testing.T) {
	hooks := &hookCapture{}
	loop := &Loop{
		hooks: hooks,
	}
	sess := &Session{ID: "test-session"}

	ch := make(chan string, 4)
	ch <- "first steer"
	ch <- "second steer"

	loop.drainSteer(ch, sess)

	msgs := sess.Snapshot()
	if got := len(msgs); got != 2 {
		t.Fatalf("expected 2 messages in session, got %d", got)
	}
	if msgs[0].Content != "first steer" || msgs[0].Role != llm.RoleUser {
		t.Errorf("first message wrong: %+v", msgs[0])
	}
	if msgs[1].Content != "second steer" || msgs[1].Role != llm.RoleUser {
		t.Errorf("second message wrong: %+v", msgs[1])
	}
	// drainSteer must NOT fire any hook — the WS handler fires it on receipt.
	if n := len(hooks.calls); n != 0 {
		t.Errorf("drainSteer fired %d hook(s) (%v); expected 0", n, hooks.calls)
	}
}

// TestDrainSteer_DropsEmptyStrings verifies that blank/whitespace-only
// messages are silently dropped and do not produce session messages.
func TestDrainSteer_DropsEmptyStrings(t *testing.T) {
	loop := &Loop{}
	sess := &Session{ID: "test-session"}

	ch := make(chan string, 4)
	ch <- "  "
	ch <- ""
	ch <- "real message"

	loop.drainSteer(ch, sess)

	msgs := sess.Snapshot()
	if got := len(msgs); got != 1 {
		t.Fatalf("expected 1 message (empty strings dropped), got %d", got)
	}
	if msgs[0].Content != "real message" {
		t.Errorf("unexpected message content: %q", msgs[0].Content)
	}
}
