package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/llm"
)

// Why: a chat turn on Claude Max has to work on a Mac that has never run one.
// The workspace does not exist until the launch script makes it, and every
// cwd the turn sends is stat'd LITERALLY by the bridge - JSON fields are not
// shell words, so nothing on the far side expands a variable.
//
// Shipping "$HOME/.infinity/brain" as that cwd made every Mac conversation
// come back as `code_agent: the mac rejected the launch request (HTTP 400):
// cwd not a directory: $HOME/.infinity/brain` (2026-08-31): a dead brain, and
// a coding-tool error printed in the middle of a conversation. Nothing caught
// it because the brain was only ever tested at the script level, where the
// string is about to be handed to bash and $HOME is fine.
func TestBrainConverseWorksOnAMacThatHasNeverRunOne(t *testing.T) {
	mac, _ := setupLocalMac(t, `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`)

	// The workspace is deliberately absent: this is a first turn.
	if _, err := os.Stat(filepath.Join(mac.home, ".infinity", "brain")); !os.IsNotExist(err) {
		t.Fatalf("the fixture must start with no brain workspace, got %v", err)
	}

	r := NewClaudeCodeRunner(bridge.NewRouter(mac, nil), nil)
	r.AttachBrain(BrainWiring{
		CoreURL:   "https://core.example.com",
		MintToken: func(string) string { return "tok-session" },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	events := make(chan llm.StreamEvent, 128)
	drained := make(chan string, 1)
	go func() {
		var text strings.Builder
		for ev := range events {
			if ev.Kind == llm.StreamText {
				text.WriteString(ev.TextDelta)
			}
		}
		drained <- text.String()
	}()

	resp, err := r.Converse(ctx, llm.BrainTurn{
		SessionID: "sess-1",
		Prompt:    "what did we decide about pricing?",
	}, events)
	close(events)
	streamed := <-drained

	if err != nil {
		t.Fatalf("a first Mac chat turn failed: %v\ncwds: %v", err, mac.cwds)
	}
	if !strings.Contains(resp.Text+streamed, "Done: edited core/x.go") {
		t.Fatalf("the turn produced no reply: resp=%q streamed=%q", resp.Text, streamed)
	}

	// The mechanic, checked directly: no cwd that leaves Infinity may carry
	// shell syntax, because nothing downstream expands it.
	mac.mu.Lock()
	cwds := append([]string(nil), mac.cwds...)
	mac.mu.Unlock()
	if len(cwds) == 0 {
		t.Fatal("the turn sent nothing to the bridge")
	}
	for _, cwd := range cwds {
		if strings.Contains(cwd, "$") {
			t.Errorf("cwd %q is shell syntax; the bridge stats it literally and answers 400", cwd)
		}
	}

	// And the workspace the brain claims to work in actually got made.
	if st, err := os.Stat(filepath.Join(mac.home, ".infinity", "brain")); err != nil || !st.IsDir() {
		t.Errorf("the launch did not create the brain workspace: %v", err)
	}
}

// Why: when the sign-in probe does fail, a conversation must not report it as
// a coding failure. "code_agent: ..." in a chat reply names a tool the boss
// never invoked and sends the model off to pass a repo path.
func TestBrainProbeFailureDoesNotSpeakAsTheCodingTool(t *testing.T) {
	err := &launchRejectedError{bridge: "mac", status: 400, detail: "cwd not a directory: /nope"}
	got := brainProbeDetail(err)
	if strings.Contains(got, "code_agent") {
		t.Errorf("a chat turn reported itself as the coding tool: %q", got)
	}
	if !strings.Contains(got, "cwd not a directory: /nope") {
		t.Errorf("the actual cause was lost: %q", got)
	}
}
