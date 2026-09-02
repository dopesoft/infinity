package sessions

import "strings"

import "testing"

// Why: this is the WHOLE contract, and both halves of it are silent when
// wrong. If AgentSelfPrompt ever lands in the conversation set, the boss's
// transcript fills with machine briefs in his own message bubble again (the
// 2026-09-01 job-hunt chat: "these fuckin markdown things popping in"). If it
// ever falls OUT of the hydration set, the model loses the reason it spoke and
// answers its own proactive message as if the boss had asked something.
func TestAgentSelfPrompt_HiddenFromHimAndKeptForTheModel(t *testing.T) {
	quoted := "'" + AgentSelfPromptHook + "'"

	if strings.Contains(ConversationHooksSQL, quoted) {
		t.Errorf("a machine-authored turn is NOT part of the conversation: %s", ConversationHooksSQL)
	}
	if strings.Contains(RenderableHooksSQL, quoted) {
		t.Errorf("a machine-authored turn must never render in the transcript: %s", RenderableHooksSQL)
	}
	if strings.Contains(HasRenderableSQL, quoted) {
		t.Error("a session holding only machine briefs is still an empty session to him")
	}
	if !strings.Contains(HydrationHooksSQL, quoted) {
		t.Errorf("the model must still be replayed its own prompt: %s", HydrationHooksSQL)
	}
	if !strings.HasPrefix(HydrationHooksSQL, ConversationHooksSQL) {
		t.Error("hydration is the conversation PLUS the machine turns, never a separate list that can drift")
	}
}
