package tools

import (
	"encoding/json"
	"testing"
)

// The context meter and the bill are different questions.
//
// The boss's report (2026-09-01): "my context is all red so it looks full, and
// has been for about an hour. Is the counter broken or what?" It was. This
// brain runs its own tool loop, so ONE of our turns is many API calls, and
// Claude Code reports the SUM. A real turn of his billed 2,172,488 cache-read
// tokens across 13 calls while the deepest single prompt - the actual window
// fill - was 172,498. Divided by a 1M window that reads 217%, so the bar
// pegged red and auto-compaction fired on a session a fifth full.
//
// The usage blob below is his, verbatim in shape.
func TestBrainUsage_MeasuresTheWindowFromTheDeepestCall(t *testing.T) {
	raw := `{
      "input_tokens": 26,
      "cache_creation_input_tokens": 33857,
      "cache_read_input_tokens": 2172488,
      "output_tokens": 7195,
      "iterations": [
        {"input_tokens": 2, "cache_read_input_tokens": 120000, "cache_creation_input_tokens": 500},
        {"input_tokens": 2, "cache_read_input_tokens": 172498, "cache_creation_input_tokens": 1061},
        {"input_tokens": 1, "cache_read_input_tokens": 90000,  "cache_creation_input_tokens": 0}
      ]
    }`
	u := brainUsage(claudeResult{RawUsage: json.RawMessage(raw)})

	// What it cost: the totals, untouched, so the ledger stays honest.
	if u.CacheRead != 2172488 || u.Input != 26 || u.Output != 7195 || u.CacheWrite != 33857 {
		t.Fatalf("the billed totals were altered: %+v", u)
	}

	// How full he is: the widest single prompt, not the sum.
	const want = 2 + 172498 + 1061
	if u.ContextTokens != want {
		t.Fatalf("window fill = %d, want %d (the deepest call, not the %d-token sum)",
			u.ContextTokens, want, u.PromptTokens())
	}
	if u.WindowTokens() != want {
		t.Fatalf("WindowTokens() = %d, want %d", u.WindowTokens(), want)
	}
	// The whole point: this must not read as a full 1M window.
	if u.WindowTokens() > 1_000_000 {
		t.Fatalf("still over a 1M window at %d tokens, so the bar stays red", u.WindowTokens())
	}
}

// A brain that answers in ONE call must be unaffected: there is no iterations
// array, and the two questions have the same answer.
func TestBrainUsage_SingleCallTurnIsUnchanged(t *testing.T) {
	raw := `{"input_tokens": 1200, "cache_read_input_tokens": 40000, "cache_creation_input_tokens": 300, "output_tokens": 88}`
	u := brainUsage(claudeResult{RawUsage: json.RawMessage(raw)})
	if u.ContextTokens != 0 {
		t.Fatalf("nothing to correct on a single-call turn, got ContextTokens=%d", u.ContextTokens)
	}
	if u.WindowTokens() != u.PromptTokens() {
		t.Fatalf("WindowTokens()=%d must fall back to PromptTokens()=%d", u.WindowTokens(), u.PromptTokens())
	}
}
