package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Why: the status file cannot be the completion signal. `claude -p` does not
// exit while its MCP servers are attached, and every serious build uses at
// least one — so on 2026-08-29 a 47-minute run that SUCCEEDED, with a full
// written report, was recorded as never finishing and announced to the boss as
// a failure. The terminal `result` event is the signal, and it must be enough
// on its own.
func TestClaudeTerminalResult_IsTheCompletionSignal(t *testing.T) {
	// The verbatim shape of the line that was ignored for 47 minutes.
	const real = `{"type":"result","subtype":"success","is_error":false,"duration_ms":2844766,"num_turns":170,"result":"Done. Here is the report…"}`
	res, ok := claudeTerminalResult(real)
	if !ok {
		t.Fatal("the terminal result event must be recognised as completion")
	}
	if res.Result != "Done. Here is the report…" || res.IsError {
		t.Fatalf("the report must survive intact: %+v", res)
	}
}

// Why: this decides that a live build is FINISHED, so a false positive throws
// away the rest of its work. It must never fire on anything but the real
// terminal object — including a half-read line, and including an event whose
// CONTENT happens to quote one (Jarvis edits this very file, so a tool_result
// carrying the words `"type":"result"` is a real thing that happens).
func TestClaudeTerminalResult_RefusesEverythingElse(t *testing.T) {
	notDone := map[string]string{
		"an assistant tool call":      `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"a.go"}}]}}`,
		"the init line":               `{"type":"system","subtype":"init","session_id":"3f2a"}`,
		"a truncated result line":     `{"type":"result","subtype":"success","is_error":false,"duration_ms":284`,
		"a result quoted in a result": `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"grep found: {\"type\":\"result\",\"is_error\":false}"}]}}`,
		"an empty stream":             "",
		"a bare object":               `{"foo":"bar"}`,
		// The shape without either field only that object carries.
		"a result-typed line with no duration or turns": `{"type":"result","result":"x"}`,
	}
	for name, line := range notDone {
		if _, ok := claudeTerminalResult(line); ok {
			t.Errorf("%s must NOT read as completion: %s", name, line)
		}
	}
}

// Why: the incremental read is what feeds the chat ledger, and it is exactly
// the kind of bookkeeping that is subtly wrong forever. Every event must be
// seen EXACTLY once: a dropped one is a step the boss never sees, a repeated
// one is a duplicate row in his transcript.
func TestConsume_ReadsEveryLineExactlyOnce(t *testing.T) {
	p := &claudePoll{line: 1}
	// A stream delivered in awkward slices: a clean one, one cut mid-line,
	// then the remainder starting with that same half line.
	var got []string
	collect := func(slice string, full bool) {
		for _, l := range strings.Split(p.consume(slice, full), "\n") {
			if strings.TrimSpace(l) != "" {
				got = append(got, l)
			}
		}
	}
	collect("one\ntwo\n", false)
	if p.line != 3 {
		t.Fatalf("after two whole lines the next read starts at line 3, got %d", p.line)
	}
	// Claude was mid-write: "three" arrived without its newline.
	collect("three", false)
	if p.line != 3 {
		t.Fatalf("a half line must not advance the read position, got %d", p.line)
	}
	// Next poll re-reads it whole, plus the next one.
	collect("three\nfour\n", false)
	if p.line != 5 {
		t.Fatalf("after four whole lines the next read starts at line 5, got %d", p.line)
	}
	want := []string{"one", "two", "three", "four"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("every line exactly once, in order: got %v want %v", got, want)
	}
}

// Why: a single line too big to ever fit the read window would otherwise wedge
// the reader on the same byte forever, and the ledger would stop dead mid-build
// with no indication anything was wrong.
func TestConsume_StepsOverALineThatCanNeverComplete(t *testing.T) {
	p := &claudePoll{line: 7}
	if got := p.consume(strings.Repeat("x", claudeChunkBytes), true); got != "" {
		t.Fatalf("a line with no newline yields nothing to parse, got %d bytes", len(got))
	}
	if p.line != 8 {
		t.Fatalf("an impossible line must be stepped over, not re-read forever: line=%d", p.line)
	}
}

// Why: the whole point of Part 3. Every tool the nested job calls has to reach
// the boss's chat as a real row, named in the vocabulary Studio already speaks,
// and it has to appear when the call STARTS — a five-minute nested `go test`
// that only shows up once it is over is the opacity this replaces.
func TestForwardSteps_SendsEachNestedCallAndItsResult(t *testing.T) {
	var (
		mu   sync.Mutex
		sent []NestedStep
	)
	p := &claudePoll{
		jobID:         "0a4074b6-655c-4837-b693-ca983d92bb47",
		parentSession: "11111111-2222-3333-4444-555555555555",
		sinkStamp:     nestedStepPrefix("0a4074b6-655c-4837-b693-ca983d92bb47"),
		setMeta:       func(string, string) {},
		sink: func(_ context.Context, s NestedStep) {
			mu.Lock()
			sent = append(sent, s)
			mu.Unlock()
		},
	}
	slice := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Edit","input":{"file_path":"studio/components/Coach.tsx","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_02","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_02","content":"ok","is_error":false}]}}`,
		"",
	}, "\n")
	p.forwardSteps(context.Background(), slice)

	if len(sent) != 3 {
		t.Fatalf("two calls and one result must all reach the chat, got %d: %+v", len(sent), sent)
	}
	if sent[0].Tool != "claude_code__edit" {
		t.Fatalf("a nested Edit must arrive in the vocabulary Studio speaks, got %q", sent[0].Tool)
	}
	if sent[0].Input["file_path"] != "studio/components/Coach.tsx" {
		t.Fatalf("the file being edited must ride the row: %+v", sent[0].Input)
	}
	if sent[0].Done {
		t.Fatal("the call half must arrive BEFORE its result, or long steps are invisible while they run")
	}
	if sent[1].Tool != "claude_code__bash" {
		t.Fatalf("a nested Bash must be named as one, got %q", sent[1].Tool)
	}
	if !sent[2].Done || sent[2].Output != "ok" || sent[2].CallID != sent[1].CallID {
		t.Fatalf("the result must upsert onto its own call's row: %+v", sent[2])
	}
	if !strings.HasPrefix(sent[0].CallID, "cc-0a4074b6-") {
		t.Fatalf("nested ids must be namespaced so a resumed pass cannot overwrite the first one's rows: %q", sent[0].CallID)
	}
	if sent[0].SessionID != p.parentSession {
		t.Fatalf("steps must be addressed to the chat that started the job, got %q", sent[0].SessionID)
	}

	// Re-reading the same slice (a retried poll, an overlapping window) must
	// not put the same step on screen twice.
	before := len(sent)
	p.forwardSteps(context.Background(), slice)
	if len(sent) != before {
		t.Fatalf("a step read twice must be sent once: %d → %d", before, len(sent))
	}
}

// Why: the nested run reaches its OWN MCP servers, and burying those under a
// second "Claude Code ·" prefix produces exactly the raw-id-shaped row the
// vocabulary layer exists to prevent.
func TestNestedToolName_KeepsMCPServersReadable(t *testing.T) {
	cases := map[string]string{
		"Edit":                       "claude_code__edit",
		"Bash":                       "claude_code__bash",
		"mcp__filesystem__read_file": "filesystem__read_file",
		"":                           "",
	}
	for in, want := range cases {
		if got := nestedToolName(in); got != want {
			t.Errorf("nestedToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Why: a verdict read after the fact is what turns "it failed" back into "it
// finished, here is the report" — and an unreachable Mac must never answer the
// same way an empty transcript does.
func TestParseClaudeVerdict_TellsDoneAliveAndCouldNotLookApart(t *testing.T) {
	done := parseClaudeVerdict(strings.Join([]string{
		"FOUND:yes", "ALIVE:yes", "RESULT:yes",
		"===LAST===",
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":2844766,"num_turns":170,"result":"Done. Here is the report."}`,
		"===TAIL===",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"core/x.go"}}]}}`,
	}, "\n"))
	if !done.Done || done.IsError || done.Report != "Done. Here is the report." {
		t.Fatalf("a finished job must report its own result: %+v", done)
	}
	// Alive AND Done together is the normal case, not a contradiction: the
	// process is held open by its MCP servers long after the work is over.
	if !done.Alive {
		t.Fatal("liveness must be reported independently of completion")
	}
	if len(done.Files) == 0 {
		t.Fatalf("the files it touched are the evidence: %+v", done)
	}

	working := parseClaudeVerdict("FOUND:yes\nALIVE:yes\nRESULT:no\n===LAST===\n===TAIL===\n")
	if working.Done || !working.Alive || !working.Looked() {
		t.Fatalf("a job still working must read as alive and unfinished: %+v", working)
	}

	dead := parseClaudeVerdict("FOUND:no\nALIVE:no\nRESULT:no\n===LAST===\n===TAIL===\n")
	if dead.Done || dead.Alive || dead.Found || !dead.Looked() {
		t.Fatalf("no files and no process is a real, successful reading: %+v", dead)
	}

	blind := parseClaudeVerdict("")
	if blind.Looked() {
		t.Fatal("a probe that answered nothing must NEVER read as an empty transcript")
	}
}
