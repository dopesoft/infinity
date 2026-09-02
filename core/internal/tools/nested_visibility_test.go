package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// A tool_use line CUT by the incremental reader's per-line cap. This is the
// shape that was being thrown away: the head of the event survives, the file
// contents it was carrying do not.
const cutWriteLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01ABCdef","name":"Write","input":{"file_path":"core/internal/pursuits/jh/store.go","content":"package jh\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"fmt\"\n\t\"time\"\n)\n\n// Store is the job hunt`

// Why: this is the difference between a worklog and three rows. The reader
// caps every line at claudeLineMaxCols, so any event carrying a file's
// contents arrives cut and fails to decode. Those were dropped in silence,
// which is why a build that edited seven files showed the boss almost nothing:
// "he obviously did things and ran commands and hit the web, but it's not
// fucking visible like when we use other models".
func TestParseNestedEvents_SalvagesACutCall(t *testing.T) {
	evs := parseNestedEvents(cutWriteLine)
	if len(evs) != 1 {
		t.Fatalf("a cut tool_use must still produce a step, got %d", len(evs))
	}
	got := evs[0]
	if got.callID != "toolu_01ABCdef" || got.tool != "claude_code__write" {
		t.Fatalf("the step's identity survives the cut: %+v", got)
	}
	if got.input["file_path"] != "core/internal/pursuits/jh/store.go" {
		t.Errorf("the file it was writing is the one thing worth showing: %+v", got.input)
	}
	if got.input["_truncated"] != true {
		t.Error("a partial record must SAY it is partial, never imply it is whole")
	}
	if got.result {
		t.Error("a salvaged line is the CALL half; salvaging a result would settle a row with contents we do not have")
	}
}

// Why: the salvage runs on every line that fails to decode, so a false match
// would invent steps out of ordinary text. A tool_use quoted inside someone
// else's output is a real thing here, because Jarvis edits this very file.
func TestParseNestedEvents_DoesNotInventSteps(t *testing.T) {
	notCalls := []string{
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"grep hit: \"type\":\"tool_use\" in code_agent_steps.go`,
		`{"type":"system","subtype":"thinking_tokens","estimated_tokens":650`,
		`{"broken`,
		"",
	}
	for _, line := range notCalls {
		if evs := parseNestedEvents(line); len(evs) != 0 {
			t.Errorf("must not invent a step from %q: %+v", line, evs)
		}
	}
}

// Why: a nested row is exempt from Studio's end-of-turn cleanup, because a
// coding job outlives its turn by design. Nothing else closed them, so when a
// job died its rows spun until the tab was reloaded - the boss's "Running 6
// commands · 15m 0s" over commands that took five seconds, and the reason Stop
// felt broken. The job must close its own steps.
func TestCloseOpenSteps_NothingIsLeftSpinning(t *testing.T) {
	var mu sync.Mutex
	var got []NestedStep
	p := &claudePoll{
		sink: func(_ context.Context, s NestedStep) {
			mu.Lock()
			got = append(got, s)
			mu.Unlock()
		},
		jobID:         "11111111-2222-4333-8444-555555555555",
		parentSession: "3f2a1c44-0000-4000-8000-000000000001",
		sinkStamp:     "cc-11111111-",
	}
	// Two calls forwarded, no results: exactly the state a killed job leaves.
	p.forwardSteps(context.Background(),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go build ./..."}}]}}`+"\n"+
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"a.go"}}]}}`)
	if len(got) != 2 {
		t.Fatalf("both calls should have been forwarded, got %d", len(got))
	}
	for _, s := range got {
		if s.Done {
			t.Fatal("a call with no result is not done yet")
		}
	}

	p.closeOpenSteps(context.Background(), "The job ended before this step reported back.")

	mu.Lock()
	defer mu.Unlock()
	closed := map[string]NestedStep{}
	for _, s := range got[2:] {
		closed[s.CallID] = s
	}
	if len(closed) != 2 {
		t.Fatalf("every open step must be closed, got %d", len(closed))
	}
	for id, s := range closed {
		if !s.Done {
			t.Errorf("%s must settle", id)
		}
		if s.IsError {
			t.Errorf("%s has no recorded outcome, which is not the same as having failed", id)
		}
		if !strings.Contains(s.Output, "ended before") {
			t.Errorf("%s must say why it has no result: %q", id, s.Output)
		}
	}
	// Idempotent: a second verdict path must not double-post.
	before := len(got)
	p.closeOpenSteps(context.Background(), "again")
	if len(got) != before {
		t.Error("closing twice must not post the rows twice")
	}
}
