package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Why: 2026-08-26. Every `claude -p` on the boss's Mac returned
// {"is_error":true,"api_error_status":429,"result":"You're out of extra usage
// · resets 6:50pm"} and code_agent booked each one as ok with the summary
// `{\` because the bridge reply was read with a first-quote scanner. The
// boss's Claude plan was spent for an hour and nothing said so.

const claudeOutOfUsage = `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"duration_ms":517,` +
	`"num_turns":1,"result":"You're out of extra usage · resets 6:50pm (America/Chicago)","stop_reason":"stop_sequence"}`

func TestBridgeBashOutput_DecodesEscapedQuotes(t *testing.T) {
	// The bridge wraps stdout in JSON, so claude's own JSON arrives escaped.
	body := []byte(`{"exit_code":1,"output":"===OUT===\n` +
		`{\"type\":\"result\",\"is_error\":true,\"result\":\"You're out of extra usage\"}\n===ERR===\n","truncated":false}`)
	out, code := bridgeBashOutput(body)
	if code != 1 {
		t.Fatalf("exit_code must decode, got %d", code)
	}
	want := "===OUT===\n{\"type\":\"result\",\"is_error\":true,\"result\":\"You're out of extra usage\"}\n===ERR===\n"
	if out != want {
		t.Fatalf("output must be the full decoded string, got %q", out)
	}
	// The exact bug, pinned: the old scanner stopped at the first escaped quote.
	if old := extractJSONFieldFast(string(body), "output"); strings.Contains(old, "is_error") {
		t.Fatalf("extractJSONFieldFast unexpectedly handles escapes now (%q); this test documents why code_agent stopped using it", old)
	}
}

func TestParseClaudeResult_SurfacesErrors(t *testing.T) {
	res := parseClaudeResult(claudeOutOfUsage)
	if !res.parsed || !res.IsError || res.APIErrorStatus != 429 {
		t.Fatalf("an is_error result must parse as an error with its upstream status: %+v", res)
	}
	if !looksLikeUsageCap(res.Result) {
		t.Fatalf("Claude's out-of-usage copy must be recognised: %q", res.Result)
	}
	ok := parseClaudeResult(`{"type":"result","subtype":"success","is_error":false,"api_error_status":null,"result":"Done: edited 3 files"}`)
	if !ok.parsed || ok.IsError || ok.Result != "Done: edited 3 files" {
		t.Fatalf("a clean result must parse as success: %+v", ok)
	}
	if parseClaudeResult("not json at all").parsed {
		t.Fatal("garbage must not claim to be parsed")
	}
}

func TestClaudeCodeHeldGuidance_NamesTheResetAndForbidsRetry(t *testing.T) {
	llm.ResetQuotaLedgerForTest()
	t.Cleanup(llm.ResetQuotaLedgerForTest)
	until := time.Now().Add(25 * time.Minute)
	msg := claudeCodeHeldGuidance(until, "You're out of extra usage · resets 6:50pm (America/Chicago)")
	for _, want := range []string{"HOLD", "out of usage", "until about", "Do NOT call code_agent", "claude_code__Edit"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("guidance missing %q:\n%s", want, msg)
		}
	}
	// No reset known: still a hold, still honest about it.
	msg = claudeCodeHeldGuidance(time.Time{}, "")
	if !strings.Contains(msg, "for now") || !strings.Contains(msg, "usage allowance is spent") {
		t.Fatalf("guidance without a reset must say so plainly:\n%s", msg)
	}
}
