package intent

import (
	"context"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// fakeProvider mimics every real provider: it emits events and returns
// WITHOUT closing the channel (the agent loop owns that). Before the fix,
// Classify blocked forever on such a provider.
type fakeProvider struct{ text string }

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake" }
func (f *fakeProvider) Stream(_ context.Context, _, _ string, _ []llm.Message, _ []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	if out != nil {
		out <- llm.StreamEvent{TextDelta: f.text}
	}
	return llm.Response{Text: f.text}, nil
}

func TestClassifyReturnsAgainstAProviderThatNeverClosesTheChannel(t *testing.T) {
	d := New(Config{Provider: &fakeProvider{text: `{"token":"full_assistance","confidence":0.9,"reason":"work order","stance":"discuss"}`}})
	done := make(chan Decision, 1)
	go func() { done <- d.Classify(context.Background(), "let's talk this through before you build", "") }()
	select {
	case dec := <-done:
		if dec.Stance != "discuss" || dec.Token != TokenFullAssistance {
			t.Fatalf("unexpected decision: %+v", dec)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Classify hung: it must return once the provider returns")
	}
}
