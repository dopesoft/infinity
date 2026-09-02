package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// A turn on a brain that runs its OWN tools must leave a trail as it goes.
//
// The boss's report (2026-09-01): "it had a message that told me why my pursuit
// was bare. I left the page, came back, and that message was gone." And,
// separately, "he has been Working for 5-10 mins and I have no idea what he's
// doing." Both are the same fault. The transcript is rebuilt from what was
// written down; on this path the only thing written mid-turn was tool RESULTS,
// so an interim message and a still-running command were both invisible to a
// reload.
//
// keepAssistantSegment's three existing call sites are all gated on
// resp.ToolCalls being non-empty, which never happens for a self-executing
// brain, so none of them could ever fire here.

// selfExecutingBrain answers the way Claude Code does: it says something, runs
// a tool INSIDE its own harness (so ToolCalls comes back empty), reports the
// result itself, and then finishes.
type selfExecutingBrain struct{ calls int }

func (p *selfExecutingBrain) Name() string       { return "claude_max" }
func (p *selfExecutingBrain) Model() string      { return "opus[1m]" }
func (p *selfExecutingBrain) RunsOwnTools() bool { return true }

func (p *selfExecutingBrain) Stream(_ context.Context, _, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	p.calls++
	out <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: "Your pursuit is bare because the roles are still Surfaced cards."}
	out <- llm.StreamEvent{
		Kind:     llm.StreamToolCall,
		ToolCall: &llm.ToolCall{ID: "toolu_1", Name: "claude_code__bash", Input: map[string]any{"command": "go build ./..."}},
	}
	out <- llm.StreamEvent{
		Kind: llm.StreamToolResult, ToolCallID: "toolu_1",
		ToolName: "claude_code__bash", ToolOutput: "ok",
	}
	// The terminal result carries only the LAST message, never the earlier
	// segments - which is why committing them mid-turn cannot duplicate them.
	return llm.Response{Text: "Done. The write path lands next."}, nil
}

// payloadHooks keeps the payload too, because "was the CALL written down while
// it was still running" is a question only the payload can answer.
type payloadHooks struct {
	mu   sync.Mutex
	rows []hookRow
}

type hookRow struct {
	name    string
	text    string
	payload map[string]any
}

func (h *payloadHooks) Emit(name, _, _, text string, payload map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append(h.rows, hookRow{name: name, text: text, payload: payload})
}

func (h *payloadHooks) named(name string) []hookRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hookRow
	for _, r := range h.rows {
		if r.name == name {
			out = append(out, r)
		}
	}
	return out
}

func runSelfExecutingTurn(t *testing.T) *payloadHooks {
	t.Helper()
	rec := &payloadHooks{}
	l := New(Config{LLM: &selfExecutingBrain{}, Tools: tools.NewRegistry(), Hooks: rec})

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)
	if err := l.Run(context.Background(), "brain-durability", "why is the pursuit bare?", "", nil, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)
	wg.Wait()
	return rec
}

// What he read before the tool ran must be in the transcript, or leaving the
// page erases it.
func TestSelfExecutingBrain_WritesDownWhatHeReadBeforeTheToolRan(t *testing.T) {
	rec := runSelfExecutingTurn(t)

	var seen []string
	for _, r := range rec.named("AssistantMessage") {
		seen = append(seen, r.text)
	}
	joined := strings.Join(seen, " | ")
	if !strings.Contains(joined, "still Surfaced cards") {
		t.Fatalf("the message he read before the tool ran was never written down, "+
			"so navigating away loses it. AssistantMessage rows: %q", joined)
	}

	// And the final message still lands, exactly once.
	final := rec.named("TaskCompleted")
	if len(final) != 1 || !strings.Contains(final[0].text, "The write path lands next") {
		t.Fatalf("the final reply is wrong or missing: %+v", final)
	}
	// The interim must not be repeated inside the final one, or he reads it twice.
	if strings.Contains(final[0].text, "still Surfaced cards") {
		t.Fatalf("the interim segment was written twice: %q", final[0].text)
	}
}

// A command that is still running must be visible to a reload, not only once
// it returns.
func TestSelfExecutingBrain_WritesDownTheCallNotOnlyTheResult(t *testing.T) {
	rec := runSelfExecutingTurn(t)

	rows := rec.named("PostToolUse")
	var running, settled int
	for _, r := range rows {
		if id, _ := r.payload["tool_call_id"].(string); id != "toolu_1" {
			continue
		}
		if on, _ := r.payload["running"].(bool); on {
			running++
			if _, has := r.payload["output"]; has {
				t.Fatal("a running row must not claim an output it does not have")
			}
			continue
		}
		settled++
	}
	if running == 0 {
		t.Fatal("the tool call was only written down once it finished, so a five-minute " +
			"command is invisible to a reload for five minutes")
	}
	if settled == 0 {
		t.Fatal("the result was never written down")
	}
}
