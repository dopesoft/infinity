package inbox

import (
	"context"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

type fakeProvider struct {
	text string
}

func (f fakeProvider) Name() string  { return "fake" }
func (f fakeProvider) Model() string { return "fake-model" }

func (f fakeProvider) Stream(_ context.Context, _ string, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	if out != nil {
		out <- llm.StreamEvent{Kind: llm.StreamText, TextDelta: f.text}
	}
	// Providers do not own or close the caller's stream channel.
	return llm.Response{Text: f.text}, nil
}

func TestClassifyDoesNotWaitForProviderToCloseStream(t *testing.T) {
	d := Deps{LLM: fakeProvider{text: `[{"index":0,"needs_reply":true,"reason":"Direct ask","importance":82}]`}}
	emails := []email{{from: "Alice <alice@example.com>", subject: "Can you review this?", snippet: "Need your input."}}

	done := make(chan map[int]decision, 1)
	go func() {
		done <- d.classify(context.Background(), emails)
	}()

	select {
	case got := <-done:
		if !got[0].NeedsReply {
			t.Fatalf("expected email 0 to need a reply, got %#v", got[0])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("classify hung waiting for the provider to close the stream")
	}
}
