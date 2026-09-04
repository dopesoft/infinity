package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// The reply a provider failure cuts short is still his reply.
//
// 2026-09-04: ChatGPT's plan ran dry mid-turn. The loop closed the turn as
// errored and wrote nothing down for the assistant side, so the only rows a
// reload found were the interim segments - and the report he had just read
// was gone from the screen. A Stop already kept the partial; an error must
// too, under the same rule.

// dyingProvider streams an answer and then fails the way a spent plan does.
type dyingProvider struct{ text string }

func (p *dyingProvider) Name() string  { return "dying" }
func (p *dyingProvider) Model() string { return "dying-1" }
func (p *dyingProvider) Stream(_ context.Context, _, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	if p.text != "" {
		out <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: p.text}
	}
	return llm.Response{}, errors.New("usage limit reached, resets 7:01am")
}

func runDyingTurn(t *testing.T, text string) (*payloadHooks, error) {
	t.Helper()
	rec := &payloadHooks{}
	l := New(Config{LLM: &dyingProvider{text: text}, Tools: tools.NewRegistry(), Hooks: rec})
	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)
	err := l.Run(context.Background(), "cut-reply", "research it", "", nil, out)
	close(out)
	wg.Wait()
	return rec, err
}

func TestAReplyCutByAProviderErrorIsWrittenDownAsHisReply(t *testing.T) {
	rec, err := runDyingTurn(t, "Boss, here is the honest 2026 read on resumes.")
	if err == nil {
		t.Fatal("the provider failure must still surface as the turn's error")
	}
	finals := rec.named("TaskCompleted")
	if len(finals) != 1 {
		t.Fatalf("expected exactly one TaskCompleted for the cut reply, got %d", len(finals))
	}
	if !strings.Contains(finals[0].text, "honest 2026 read") {
		t.Fatalf("the cut reply was not the text he read: %q", finals[0].text)
	}
	if v, _ := finals[0].payload["interrupted"].(bool); !v {
		t.Fatalf("a cut reply is filed interrupted, got payload %+v", finals[0].payload)
	}
	if v, _ := finals[0].payload["errored"].(bool); !v {
		t.Fatalf("a reply cut by an error says so, got payload %+v", finals[0].payload)
	}
	if _, ok := finals[0].payload["message_index"]; !ok {
		t.Fatalf("the cut reply must carry its message index so the browser pairs it with the bubble: %+v", finals[0].payload)
	}
}

func TestAnErrorBeforeAnyTextWritesNoEmptyReply(t *testing.T) {
	rec, _ := runDyingTurn(t, "")
	if n := len(rec.named("TaskCompleted")); n != 0 {
		t.Fatalf("an errored turn with nothing said must not file an empty reply, got %d", n)
	}
}

func TestCutReplyText_KeepsTheAnswerNotTheHealPreamble(t *testing.T) {
	// A self-heal pass was pending when the provider died: the answer he was
	// waiting for is the one before the pass, not the pass's own notes.
	got := cutReplyText("Let me check what went wrong...", false, 0, true, "Here is your report.", "")
	if got != "Here is your report." {
		t.Fatalf("got %q", got)
	}
	// A verify pass was running: the answer plus the caveat, as the clean end does.
	got = cutReplyText("One weak spot: the citations.", false, 0, false, "", "The full report, ten paragraphs long, with everything he asked for and more besides, laid out in order.")
	if !strings.Contains(got, "The full report") || !strings.Contains(got, "weak spot") {
		t.Fatalf("verify merge lost part of the reply: %q", got)
	}
	// A self-executing brain: only the tail past what was already written down.
	got = cutReplyText("already kept.new words", true, len("already kept."), false, "", "")
	if got != "new words" {
		t.Fatalf("got %q", got)
	}
}
