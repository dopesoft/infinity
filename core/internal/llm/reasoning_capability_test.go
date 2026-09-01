package llm

import "testing"

// Why: the per-turn effort router clamps to "no hint" for any model it thinks
// cannot reason, and it asked a check that only knew OpenAI's ids. The plan
// brain reports itself as "opus[1m]", so the router concluded Claude does not
// reason, never sized the thinking, and left it deliberating at the box's own
// default: 9,200 reasoning tokens before a 1,200-token answer, and 2m23s of
// spinner on a conversational question (2026-09-01).
func TestEveryBrainsReasoningIsRecognised(t *testing.T) {
	reasons := []string{
		"opus[1m]", "opus", "sonnet", "haiku", "fable",
		"claude-opus-5", "claude-sonnet-5", "claude_max:opus[1m]",
		"gpt-5.6-sol", "gpt-5", "o3", "o1-mini",
	}
	for _, m := range reasons {
		if !ModelSupportsReasoning(m) {
			t.Errorf("%q reasons, but the router would send it no effort level", m)
		}
	}
	for _, m := range []string{"deepseek-v4-pro", "gpt-4o", "gemini-2.5-pro", ""} {
		if IsClaudeModel(m) {
			t.Errorf("%q is not a Claude id", m)
		}
	}
}
