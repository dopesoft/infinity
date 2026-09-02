package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// The brain reaches the loop WRAPPED, and the tests before this one did not.
//
// factory.go wraps every registered provider in noDashesProvider, and the
// failover layer wraps that again. The loop asked `provider.(SelfExecutingProvider)`
// on the wrapper, which does not implement it, so for every Claude Max turn
// the boss ever had the answer was "this brain does not run tools" and the
// branch that records its tool calls, results and interim messages never
// executed. He watched it write store.go to his tree and saw nothing in the
// chat (2026-09-02).
//
// brain_durability_test.go proved the branch works when it RUNS. It passed
// against production code that never ran it, because its fake brain was
// handed to the loop bare. This test hands it over the way production does.
func TestSelfExecutingBrain_StillRecognisedThroughTheWrappers(t *testing.T) {
	rec := &payloadHooks{}
	wrapped := llm.WrapNoDashes(&selfExecutingBrain{})
	l := New(Config{LLM: wrapped, Tools: tools.NewRegistry(), Hooks: rec})

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)
	if err := l.Run(context.Background(), "brain-wrapped", "why is the pursuit bare?", "", nil, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)
	wg.Wait()

	if len(rec.named("PostToolUse")) == 0 {
		t.Fatal("the brain arrived wrapped and the loop stopped recognising it: " +
			"no tool row was written, which is exactly what production did")
	}
	var seen []string
	for _, r := range rec.named("AssistantMessage") {
		seen = append(seen, r.text)
	}
	if !strings.Contains(strings.Join(seen, " | "), "still Surfaced cards") {
		t.Fatalf("the interim message was not written down through the wrapper: %q", seen)
	}
}

// And the helper itself, at every depth the registry can produce.
func TestSelfExecuting_UnwrapsToTheRealBrain(t *testing.T) {
	brain := &selfExecutingBrain{}
	if !llm.SelfExecuting(brain) {
		t.Fatal("bare brain not recognised")
	}
	if !llm.SelfExecuting(llm.WrapNoDashes(brain)) {
		t.Fatal("one wrapper hid the brain; this is the production shape")
	}
	if !llm.SelfExecuting(llm.WrapNoDashes(llm.WrapNoDashes(brain))) {
		t.Fatal("two wrappers hid the brain")
	}
	if llm.SelfExecuting(llm.WrapNoDashes(&twoSegmentProvider{})) {
		t.Fatal("an ordinary brain must not be mistaken for a self-executing one")
	}
	if llm.SelfExecuting(nil) {
		t.Fatal("nil is not a brain")
	}
}
