package proactive

import "testing"

// The 12 un-dismissable curiosity questions all came from detectors treating
// non-actionable tools as if a prompt-rework / self-heal / skill-recipe
// question made sense. These tests pin the classification so a future edit
// can't silently re-admit the noise.
func TestClassifyTool(t *testing.T) {
	cases := []struct {
		tool string
		want ToolClass
	}{
		{"claude_code__Bash", ClassCodingBridge},
		{"claude_code__Read", ClassCodingBridge},
		{"compact_context", ClassInternal},
		{"recall", ClassInternal},
		{"skills_history", ClassInternal},
		{"memory_search", ClassInternal},
		{"mem_get", ClassInternal},
		{"composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", ClassDataFetch},
		{"composio__GMAIL_FETCH_MESSAGE_BY_THREAD_ID", ClassDataFetch},
		{"httpfetch", ClassDataFetch},
		{"websearch", ClassDataFetch},
		// Actionable: skills/workflows where reworking the prompt is real.
		{"skills_invoke_recipe", ClassActionable},
		{"workflow_run", ClassActionable},
		{"some_agent_tool", ClassActionable},
	}
	for _, c := range cases {
		if got := ClassifyTool(c.tool); got != c.want {
			t.Errorf("ClassifyTool(%q) = %v, want %v", c.tool, got, c.want)
		}
	}
}

func TestEligibilityHelpers(t *testing.T) {
	// high_surprise: only actionable tools — the 8 high_surprise rows must all
	// be suppressed.
	for _, tool := range []string{
		"compact_context", "recall", "skills_history",
		"composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", "claude_code__Bash",
	} {
		if EligibleForHighSurprise(tool) {
			t.Errorf("EligibleForHighSurprise(%q) = true, want false (noise)", tool)
		}
	}
	if !EligibleForHighSurprise("some_agent_tool") {
		t.Error("EligibleForHighSurprise(actionable) = false, want true")
	}

	// repeated_tool_error: claude_code excluded (normal coding non-zero exits),
	// connector failures still eligible.
	if EligibleForRepeatedError("claude_code__Bash") {
		t.Error("EligibleForRepeatedError(claude_code__Bash) = true, want false")
	}
	if !EligibleForRepeatedError("composio__GMAIL_SEND") {
		t.Error("EligibleForRepeatedError(composio) = false, want true (auth/api failure is actionable)")
	}

	// skill_pattern: claude_code + internal are non-substantive.
	if SubstantiveForSkillPattern("claude_code__Read") {
		t.Error("SubstantiveForSkillPattern(claude_code__Read) = true, want false")
	}
	if SubstantiveForSkillPattern("compact_context") {
		t.Error("SubstantiveForSkillPattern(compact_context) = true, want false")
	}
}
