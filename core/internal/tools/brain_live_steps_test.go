package tools

import (
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// The VERBATIM shape Claude Code streams while it works, taken from the boss's
// own turn on 2026-09-01: it names the tool the moment it decides on one, then
// types the arguments in. This path used to read neither, and waited for the
// assembled `assistant` message that only lands once the whole block is
// finished - so he watched a bare "Thinking" for two minutes while Claude was
// telling us, second by second, what it was doing. "so i just sit there watch
// it reasoning with no context?? unlike my other models".
const liveToolStream = `{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_01UjQ","name":"Bash","input":{}}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"command\": \"git log"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":" --oneline -3\"}"}}}
{"type":"stream_event","event":{"type":"content_block_stop"}}`

func drain(p *brainPoll, ch chan llm.StreamEvent, slice string) []llm.StreamEvent {
	p.out = ch
	p.emit(slice)
	close(ch)
	var got []llm.StreamEvent
	for ev := range ch {
		got = append(got, ev)
	}
	return got
}

// Why: the row has to appear when Claude DECIDES on the tool, not when it has
// finished writing the arguments. That gap is the whole complaint.
func TestBrainEmit_ShowsTheToolAsItIsWritten(t *testing.T) {
	p := &brainPoll{}
	got := drain(p, make(chan llm.StreamEvent, 32), liveToolStream)

	if len(got) == 0 || got[0].Kind != llm.StreamToolCall {
		t.Fatalf("the tool must be announced first, got %+v", got)
	}
	if got[0].ToolCall == nil || got[0].ToolCall.Name != "claude_code__bash" {
		t.Fatalf("named in the vocabulary Studio speaks: %+v", got[0].ToolCall)
	}

	var args strings.Builder
	for _, ev := range got[1:] {
		if ev.Kind != llm.StreamToolInputDelta {
			t.Fatalf("only argument deltas should follow: %+v", ev)
		}
		if ev.ToolCallID != "toolu_01UjQ" {
			t.Errorf("an argument delta must be attributed to its call: %+v", ev)
		}
		args.WriteString(ev.InputDelta)
	}
	if !strings.Contains(args.String(), "git log --oneline -3") {
		t.Errorf("he must see the actual command as it is typed: %q", args.String())
	}
}

// Why: the assembled `assistant` message repeats every call the live path
// already sent, under the SAME id. Without the guard he gets two rows for one
// command, which is worse than the silence it replaced.
func TestBrainEmit_DoesNotPostTheSameToolTwice(t *testing.T) {
	p := &brainPoll{}
	assembled := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01UjQ","name":"Bash","input":{"command":"git log --oneline -3"}}]}}`
	got := drain(p, make(chan llm.StreamEvent, 32), liveToolStream+"\n"+assembled)

	calls := 0
	for _, ev := range got {
		if ev.Kind == llm.StreamToolCall {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("one command, one row: got %d call events", calls)
	}
}

// Why: the RESULT still has to come off the assembled message - it is the only
// place it appears - and it must still be attributed to the tool he saw.
func TestBrainEmit_StillDeliversTheResult(t *testing.T) {
	p := &brainPoll{}
	result := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01UjQ","content":"87b58bc Enhance nested job checklist"}]}}`
	got := drain(p, make(chan llm.StreamEvent, 32), liveToolStream+"\n"+result)

	var res *llm.StreamEvent
	for i := range got {
		if got[i].Kind == llm.StreamToolResult {
			res = &got[i]
		}
	}
	if res == nil {
		t.Fatal("the result must still arrive")
	}
	if res.ToolName != "claude_code__bash" {
		t.Errorf("under the name he saw on the call, got %q", res.ToolName)
	}
	if !strings.Contains(res.ToolOutput, "87b58bc") {
		t.Errorf("carrying what came back: %q", res.ToolOutput)
	}
}

// Why: a brain that reports HOW MUCH it is reasoning rather than what (Claude
// Code redacts the text) still has to prove it is alive.
func TestBrainEmit_ForwardsReasoningProgress(t *testing.T) {
	p := &brainPoll{}
	got := drain(p, make(chan llm.StreamEvent, 8),
		`{"type":"system","subtype":"thinking_tokens","estimated_tokens":650}`)
	if len(got) != 1 || got[0].Kind != llm.StreamThinking || got[0].ThinkingTokens != 650 {
		t.Fatalf("the reasoning count must reach the row: %+v", got)
	}
}
