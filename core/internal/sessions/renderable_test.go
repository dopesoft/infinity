package sessions

import "strings"

import "testing"

// Why: two consumers read this list and they must never disagree - the
// transcript Studio renders, and the history Core replays to the model when a
// session is faulted back into memory. On 2026-09-01 a hook was added for the
// UI while the model's rebuild kept its own hardcoded copy, so two of Jarvis's
// own answers became invisible to him and the next turn re-answered a point
// from three messages earlier.
func TestRenderableIsTheConversationPlusToolCards(t *testing.T) {
	if !strings.HasPrefix(RenderableHooksSQL, ConversationHooksSQL) {
		t.Fatalf("the two lists have drifted apart:\n  conversation: %s\n  renderable:   %s",
			ConversationHooksSQL, RenderableHooksSQL)
	}
	// Every kind of message he can be SHOWN has to be replayable TO the model,
	// or he and the brain are reading different conversations.
	for _, hook := range []string{"UserPromptSubmit", "TaskCompleted", "AssistantMessage", "DashboardSeed"} {
		if !strings.Contains(ConversationHooksSQL, "'"+hook+"'") {
			t.Errorf("%s is part of the conversation but not in the list the model is replayed", hook)
		}
	}
	// Tool cards are a UI concern; the model gets its tool results through the
	// message list itself, not through these rows.
	for _, hook := range []string{"PostToolUse", "PostToolUseFailure"} {
		if strings.Contains(ConversationHooksSQL, "'"+hook+"'") {
			t.Errorf("%s should not be replayed to the model as a message", hook)
		}
		if !strings.Contains(RenderableHooksSQL, "'"+hook+"'") {
			t.Errorf("%s must still render in the transcript", hook)
		}
	}
}
