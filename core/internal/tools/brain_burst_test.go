package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// The Claude brain reads its transcript on a poll and emits everything new at
// once, so one read routinely carries more events than the agent loop's
// channel can hold. These tests pin the consequence of that, because the old
// non-blocking send made a full buffer indistinguishable from a finished turn:
// the boss watched a spinner for twenty minutes on a turn that had answered.

// burstStream builds a stream slice with n text deltas followed by the reply,
// mirroring the shape a real poll returns.
func burstStream(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		ev := map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("chunk-%d ", i)},
			},
		}
		line, _ := json.Marshal(ev)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestBrainSend_DeliversABurstBiggerThanTheBuffer.
//
// 200 events into a 64-slot channel, which is exactly the live shape: the
// loop's buffer is 64 and one poll of a working turn carried well over a
// hundred. Every event must arrive. Under the old `default:` drop this test
// loses about two thirds of them.
func TestBrainSend_DeliversABurstBiggerThanTheBuffer(t *testing.T) {
	const n = 200
	events := make(chan llm.StreamEvent, 64)
	p := &brainPoll{out: events, toolNames: map[string]string{}, sentCalls: map[string]bool{}}

	// A consumer that reads steadily but not instantly, like the agent loop:
	// it does real work per event.
	got := make(chan int, 1)
	go func() {
		count := 0
		for range events {
			count++
			time.Sleep(50 * time.Microsecond)
		}
		got <- count
	}()

	p.emit(burstStream(n))
	close(events)

	select {
	case count := <-got:
		if count != n {
			t.Fatalf("consumer saw %d of %d events; %d were thrown away", count, n, n-count)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("burst never finished")
	}
}

// TestBrainFinish_AlwaysDeliversTheCompletion.
//
// The completion is the frame that ends the spinner. Dropping a text delta
// costs a word on screen; dropping this one leaves a finished turn looking
// live forever, which is the exact failure the boss reported. It has to
// survive a channel that is already full when finish() runs.
func TestBrainFinish_AlwaysDeliversTheCompletion(t *testing.T) {
	events := make(chan llm.StreamEvent, 2)
	p := &brainPoll{out: events, toolNames: map[string]string{}, sentCalls: map[string]bool{}}

	// Fill it, so finish() has to wait for room rather than find a slot.
	events <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: "a"}
	events <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: "b"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.finish(claudeResult{Result: "the answer"})
		close(events)
	}()

	var kinds []llm.StreamEventKind
	for ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	<-done

	var sawComplete bool
	for _, k := range kinds {
		if k == llm.StreamComplete {
			sawComplete = true
		}
	}
	if !sawComplete {
		t.Fatalf("the turn ended without telling anyone: got %v", kinds)
	}
}

// TestBrainEmit_NamesEditsInStudiosVocabulary.
//
// Claude Code names its own tools bare - "Edit", "Write", "Bash", "Read" -
// while Studio's whole vocabulary, its glyphs, its diff view, its Changes
// column and isCodeChangeTool are all keyed on the `claude_code__*` names the
// MCP bridge produces for the SAME tools. An unmapped name falls through to a
// generic "Working" row with no file, no diff and no Canvas: which is exactly
// what "I never see it editing files like other models" looks like.
//
// So the mapping is not cosmetic and it is pinned on the live path.
func TestBrainEmit_NamesEditsInStudiosVocabulary(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_edit","name":"Edit"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_write","name":"Write"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_bash","name":"Bash"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_read","name":"Read"}}}`,
	}, "\n")

	events := make(chan llm.StreamEvent, 32)
	p := &brainPoll{out: events, toolNames: map[string]string{}, sentCalls: map[string]bool{}}
	p.emit(stream)
	close(events)

	got := map[string]string{}
	for ev := range events {
		if ev.Kind == llm.StreamToolCall && ev.ToolCall != nil {
			got[ev.ToolCall.ID] = ev.ToolCall.Name
		}
	}
	want := map[string]string{
		"toolu_edit":  "claude_code__edit",
		"toolu_write": "claude_code__write",
		"toolu_bash":  "claude_code__bash",
		"toolu_read":  "claude_code__read",
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("call %s was named %q, Studio needs %q to draw the file row", id, got[id], name)
		}
	}
}
