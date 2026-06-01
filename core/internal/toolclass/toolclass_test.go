package toolclass

import "testing"

// The 12 un-dismissable curiosity questions (and the poisoned Gym corpus) all
// came from consumers treating non-actionable tools as if a prompt-rework /
// self-heal / skill-recipe / curriculum signal made sense. These tests pin the
// classification so a future edit can't silently re-admit the noise.
func TestClassify(t *testing.T) {
	cases := []struct {
		tool string
		want ToolClass
	}{
		{"claude_code__Bash", ClassShellFileOps},
		{"claude_code__Read", ClassShellFileOps},
		{"compact_context", ClassInternal},
		{"recall", ClassInternal},
		{"skills_history", ClassInternal},
		{"memory_search", ClassInternal},
		{"mem_get", ClassInternal},
		{"composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", ClassDataFetch},
		{"composio__GMAIL_FETCH_MESSAGE_BY_THREAD_ID", ClassDataFetch},
		{"httpfetch", ClassDataFetch},
		{"websearch", ClassDataFetch},
		{"skills_invoke_recipe", ClassActionable},
		{"workflow_run", ClassActionable},
		{"some_agent_tool", ClassActionable},
	}
	for _, c := range cases {
		if got := Classify(c.tool); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.tool, got, c.want)
		}
	}
}

func TestEligibilityHelpers(t *testing.T) {
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
	if EligibleForRepeatedError("claude_code__Bash") {
		t.Error("EligibleForRepeatedError(claude_code__Bash) = true, want false")
	}
	if !EligibleForRepeatedError("composio__GMAIL_SEND") {
		t.Error("EligibleForRepeatedError(composio) = false, want true")
	}
	if SubstantiveForSkillPattern("claude_code__Read") {
		t.Error("SubstantiveForSkillPattern(claude_code__Read) = true, want false")
	}
	if SubstantiveForSkillPattern("compact_context") {
		t.Error("SubstantiveForSkillPattern(compact_context) = true, want false")
	}
}
