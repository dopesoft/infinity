package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// fakeProvider returns the same canned Response on every Stream call. The loop
// reads tool calls off the returned Response (not the streamed events), so the
// fake never has to emit stream events — returning a Response with a ToolCall
// makes the loop run forever (it never sees an empty-tool-call "final answer"),
// which is exactly the shape that exhausts the per-segment budget.
type fakeProvider struct {
	resp llm.Response
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake-1" }
func (f *fakeProvider) Stream(ctx context.Context, model, system string, msgs []llm.Message, tools []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	return f.resp, nil
}

// countingTool records how many times it ran and can be configured to always
// succeed (progress) or always error (thrash).
type countingTool struct {
	calls atomic.Int64
	fail  bool
}

func (t *countingTool) Name() string           { return "noop" }
func (t *countingTool) Description() string    { return "test tool" }
func (t *countingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *countingTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	t.calls.Add(1)
	if t.fail {
		return "", context.DeadlineExceeded // any non-nil error
	}
	return "ok", nil
}

// drain consumes RunEvents so the loop never blocks on the unbuffered emit, and
// returns the last error event's text.
func drain(out <-chan RunEvent, done *sync.WaitGroup, lastErr *string) {
	defer done.Done()
	for ev := range out {
		if ev.Kind == EventError {
			*lastErr = ev.Error
		}
	}
}

func newTestLoop(t *testing.T, tool tools.Tool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(tool)
	l := New(Config{
		LLM:   &fakeProvider{resp: llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "noop", Input: map[string]any{}}}}},
		Tools: reg,
	})
	return l
}

// TestContinuationRunsMultipleSegments verifies that a turn which keeps making
// progress but never finishes runs maxTurnSegments × maxToolIterations tool
// calls before pulling up — i.e. it checkpoints and continues past the
// per-segment cap instead of hard-erroring at the first cap (the old behavior).
func TestContinuationRunsMultipleSegments(t *testing.T) {
	t.Setenv("INFINITY_MAX_TOOL_ITERATIONS", "2")
	t.Setenv("INFINITY_MAX_TURN_SEGMENTS", "3")

	tool := &countingTool{}
	l := newTestLoop(t, tool)

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)

	err := l.Run(context.Background(), "test-session", "go", "", nil, out)
	close(out)
	wg.Wait()

	if err == nil {
		t.Fatal("expected the loop to error after exhausting all segments")
	}
	if !strings.Contains(err.Error(), "iteration cap") {
		t.Fatalf("error should keep the iteration-cap phrasing for Humanize, got %q", err.Error())
	}
	// 2 iterations/segment × 3 segments = 6 tool calls before giving up.
	if got := tool.calls.Load(); got != 6 {
		t.Fatalf("expected 6 tool calls across 3 segments, got %d", got)
	}
}

// TestNoProgressSegmentStops verifies the thrash guard: a segment whose tool
// calls all error is not continued — the loop stops after a single segment
// rather than burning the full segment budget spinning on failures.
func TestNoProgressSegmentStops(t *testing.T) {
	t.Setenv("INFINITY_MAX_TOOL_ITERATIONS", "2")
	t.Setenv("INFINITY_MAX_TURN_SEGMENTS", "3")

	tool := &countingTool{fail: true}
	l := newTestLoop(t, tool)

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	var lastErr string
	wg.Add(1)
	go drain(out, &wg, &lastErr)

	err := l.Run(context.Background(), "test-session", "go", "", nil, out)
	close(out)
	wg.Wait()

	if err == nil {
		t.Fatal("expected the loop to error")
	}
	// One segment of 2 iterations, all failing → no successes → no continuation.
	if got := tool.calls.Load(); got != 2 {
		t.Fatalf("a zero-progress segment must not continue: expected 2 tool calls, got %d", got)
	}
}
