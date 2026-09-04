package tools

import (
	"context"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/plan"
)

// ownSink records what the chat brain mirrors onto the conversation's plan.
type ownSink struct {
	owns []struct {
		session, title string
		items          []plan.ChecklistItem
	}
}

func (s *ownSink) Sync(context.Context, NestedChecklist) error { return nil }
func (s *ownSink) Settle(context.Context, string, bool, string) error {
	return nil
}
func (s *ownSink) SyncOwn(_ context.Context, sessionID, title string, items []plan.ChecklistItem) error {
	s.owns = append(s.owns, struct {
		session, title string
		items          []plan.ChecklistItem
	}{sessionID, title, items})
	return nil
}

const brainTodoSlice = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":[{"content":"Read the cockpit","status":"in_progress","activeForm":"Reading"},{"content":"Fix the scroll","status":"pending","activeForm":"Fixing"}]}}]}}`

// The chat brain's TodoWrite used to reach nobody: the mirror hung off the
// coding job's poller only, so a conversation that laid out its steps showed
// the boss no dock to track them by (2026-09-04).
func TestBrain_OwnChecklistLandsOnTheConversationsPlan(t *testing.T) {
	sink := &ownSink{}
	p := &brainPoll{
		plans:         sink,
		parentSession: "11111111-2222-3333-4444-555555555555",
		turn:          llm.BrainTurn{Prompt: "some system preamble\n\nfix the kanban scroll on my job hunt board"},
	}
	p.syncOwnPlan(brainTodoSlice)
	if len(sink.owns) != 1 {
		t.Fatalf("want one mirrored checklist, got %d", len(sink.owns))
	}
	got := sink.owns[0]
	if got.session != p.parentSession {
		t.Fatalf("mirrored onto %q, want the conversation", got.session)
	}
	if got.title != "fix the kanban scroll on my job hunt board" {
		t.Fatalf("title = %q, want the boss's ask", got.title)
	}
	if len(got.items) != 2 || got.items[0].Status != plan.StepInProgress || got.items[1].Status != plan.StepPending {
		t.Fatalf("items = %+v", got.items)
	}
	// The same list again is not rewritten: the dock would flicker on every
	// poll otherwise.
	p.syncOwnPlan(brainTodoSlice)
	if len(sink.owns) != 1 {
		t.Fatalf("an unchanged checklist must not be rewritten, got %d writes", len(sink.owns))
	}
}

func TestBrain_NoDockForASubAgentOrAnUnwiredSink(t *testing.T) {
	p := &brainPoll{parentSession: "11111111-2222-3333-4444-555555555555"}
	p.syncOwnPlan(brainTodoSlice) // nil sink: must not panic
	sink := &ownSink{}
	p = &brainPoll{plans: sink, parentSession: "subagent:abc"}
	p.syncOwnPlan(brainTodoSlice)
	if len(sink.owns) != 0 {
		t.Fatal("an ephemeral sub-agent session has no dock; nothing may be mirrored for it")
	}
}
