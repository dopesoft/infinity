package sentinel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeAgent captures the prompt an agent_turn dispatch hands it. RunAgentTurn
// runs in a goroutine (dispatch detaches it), so we signal via a channel.
type fakeAgent struct {
	got chan string
}

func (f *fakeAgent) RunAgentTurn(_ context.Context, _ string, prompt string) error {
	f.got <- prompt
	return nil
}

// agent_turn is the path that makes "ping me when X" work: the instruction is
// run through the agent loop, and the event detail (what tripped) is appended
// so the agent can act on the specifics. This test pins both behaviours.
func TestDispatch_AgentTurn_RunsPromptWithPayload(t *testing.T) {
	fa := &fakeAgent{got: make(chan string, 1)}
	d := SkillDispatcher{Agent: fa}

	chain, _ := json.Marshal([]Action{
		{Kind: "agent_turn", Args: map[string]any{"prompt": "Notify the boss that unread email topped 20."}},
	})
	s := Sentinel{Name: "inbox-alarm", ActionChain: chain}

	if err := d.Dispatch(context.Background(), s, map[string]any{"unread": 23}); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	select {
	case prompt := <-fa.got:
		if !strings.Contains(prompt, "Notify the boss") {
			t.Errorf("prompt missing instruction: %q", prompt)
		}
		// The event detail must be appended so the agent knows the specifics.
		if !strings.Contains(prompt, "unread") || !strings.Contains(prompt, "23") {
			t.Errorf("prompt missing trigger payload detail: %q", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent runner was never invoked")
	}
}

// A missing agent runner must not silently swallow an agent_turn — it should
// surface (falling back to the inner logger when present, else erroring).
func TestDispatch_AgentTurn_NoRunner_Errors(t *testing.T) {
	d := SkillDispatcher{} // no Agent, no Inner
	chain, _ := json.Marshal([]Action{
		{Kind: "agent_turn", Args: map[string]any{"prompt": "do a thing"}},
	})
	err := d.Dispatch(context.Background(), Sentinel{Name: "x", ActionChain: chain}, nil)
	if err == nil {
		t.Fatal("expected an error when no agent runner is configured")
	}
}

// An empty prompt is a malformed action — fail loud, don't run a blank turn.
func TestDispatch_AgentTurn_EmptyPrompt_Errors(t *testing.T) {
	d := SkillDispatcher{Agent: &fakeAgent{got: make(chan string, 1)}}
	chain, _ := json.Marshal([]Action{{Kind: "agent_turn", Args: map[string]any{}}})
	if err := d.Dispatch(context.Background(), Sentinel{Name: "x", ActionChain: chain}, nil); err == nil {
		t.Fatal("expected an error for an agent_turn with no prompt")
	}
}
