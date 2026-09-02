package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// A real slice of Claude Code's stream-json: it lays out a checklist, does a
// step, then resends the WHOLE list with the first item ticked. The verbatim
// shape matters — `content` + `activeForm`, not `text`, is what Claude Code
// actually writes, and reading the wrong key is how this would compile, pass a
// hand-written test, and mirror nothing in production.
const nestedTodoStream = `{"type":"system","subtype":"init","session_id":"3f2a1c44-0000-4000-8000-000000000001"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":[{"content":"Read the dock","status":"in_progress","activeForm":"Reading the dock"},{"content":"Mirror the checklist","status":"pending","activeForm":"Mirroring the checklist"}]}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Todos have been modified successfully."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"studio/components/BackgroundJobDock.tsx"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"TodoWrite","input":{"todos":[{"content":"Read the dock","status":"completed","activeForm":"Reading the dock"},{"content":"Mirror the checklist","status":"in_progress","activeForm":"Mirroring the checklist"}]}}]}}`

// Why: this is the whole feature. Claude Code keeps its own checklist and
// resends the complete list on every call, so the LAST TodoWrite in a slice is
// the current state and every earlier one is history. Taking the first would
// pin the dock to a checklist that was already stale when it was read.
func TestNewestNestedChecklist_TakesTheLatestList(t *testing.T) {
	items, ok := newestNestedChecklist(nestedTodoStream)
	if !ok {
		t.Fatal("a stream containing TodoWrite calls must yield a checklist")
	}
	if len(items) != 2 {
		t.Fatalf("want both todos, got %d: %+v", len(items), items)
	}
	if items[0].Text != "Read the dock" || items[0].Status != plan.StepDone {
		t.Errorf("the newest list has step 1 finished, got %+v", items[0])
	}
	if items[1].Text != "Mirror the checklist" || items[1].Status != plan.StepInProgress {
		t.Errorf("the newest list has step 2 running, got %+v", items[1])
	}
}

// Why: a stream with no checklist in it must leave the plan alone rather than
// write an empty one. Most polls of a long build carry only file reads.
func TestNewestNestedChecklist_SilentWithoutATodoWrite(t *testing.T) {
	const noTodos = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t9","name":"Bash","input":{"command":"go build ./..."}}]}}`
	if _, ok := newestNestedChecklist(noTodos); ok {
		t.Fatal("a stream with no TodoWrite must not produce a checklist")
	}
	if _, ok := newestNestedChecklist(""); ok {
		t.Fatal("an empty slice must not produce a checklist")
	}
}

// Why: the two agents write the same list two different ways. Claude Code says
// `content`, Infinity's own todo_write says `text`, and both say "completed"
// where a plan step says "done". One mapping, so the dock cannot tell which
// agent authored the list it is drawing.
func TestChecklistFromTodos_ReadsBothShapes(t *testing.T) {
	items := checklistFromTodos(map[string]any{"todos": []any{
		map[string]any{"content": "Claude Code's shape", "status": "completed"},
		map[string]any{"text": "Infinity's shape", "status": "in_progress"},
		map[string]any{"content": "No status at all"},
		map[string]any{"content": "   ", "status": "pending"}, // blank: dropped
		"not an object", // junk: dropped
	}})
	if len(items) != 3 {
		t.Fatalf("want the three real items, got %d: %+v", len(items), items)
	}
	want := []plan.ChecklistItem{
		{Text: "Claude Code's shape", Status: plan.StepDone},
		{Text: "Infinity's shape", Status: plan.StepInProgress},
		{Text: "No status at all", Status: plan.StepPending},
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
}

// Why: a build polls every 15 seconds for up to 40 minutes and calls TodoWrite
// a handful of times. Without the fingerprint, every poll after the first would
// rewrite an unchanged plan — deleting and reinserting every step row, which
// the dock and the work board both watch over realtime.
func TestChecklistFingerprint_ChangesOnlyOnRealChange(t *testing.T) {
	a := []plan.ChecklistItem{{Text: "one", Status: plan.StepPending}}
	same := []plan.ChecklistItem{{Text: "one", Status: plan.StepPending}}
	ticked := []plan.ChecklistItem{{Text: "one", Status: plan.StepDone}}
	if checklistFingerprint(a) != checklistFingerprint(same) {
		t.Error("an identical list must fingerprint identically")
	}
	if checklistFingerprint(a) == checklistFingerprint(ticked) {
		t.Error("ticking a box IS the change the dock exists to show")
	}
}

// recordingSink captures what the poll loop hands the plan substrate.
type recordingSink struct {
	syncs   []NestedChecklist
	err     error
	settles []struct {
		runID  string
		failed bool
	}
}

func (r *recordingSink) Sync(_ context.Context, c NestedChecklist) error {
	r.syncs = append(r.syncs, c)
	return r.err
}

func (r *recordingSink) Settle(_ context.Context, runID string, failed bool, _ string) error {
	r.settles = append(r.settles, struct {
		runID  string
		failed bool
	}{runID, failed})
	return nil
}

func testPoll(sink *recordingSink) *claudePoll {
	return &claudePoll{
		plans:         sink,
		jobID:         "11111111-2222-4333-8444-555555555555",
		parentSession: "3f2a1c44-0000-4000-8000-000000000001",
		label:         "Claude Code: wire the dock",
	}
}

// Why: the mirror must carry the run id (which is what OWNS the plan) and the
// run's own label (so the dock keeps the headline it was already showing
// instead of renaming itself the moment the first checklist lands).
func TestSyncNestedPlan_MirrorsOnceAndAddressesTheChat(t *testing.T) {
	sink := &recordingSink{}
	p := testPoll(sink)

	p.syncNestedPlan(context.Background(), nestedTodoStream)
	if len(sink.syncs) != 1 {
		t.Fatalf("want one mirror, got %d", len(sink.syncs))
	}
	got := sink.syncs[0]
	if got.SessionID != p.parentSession || got.RunID != p.jobID || got.Title != p.label {
		t.Errorf("the mirror must be addressed to the chat and owned by the run: %+v", got)
	}

	// The same slice again: nothing changed, so nothing is rewritten.
	p.syncNestedPlan(context.Background(), nestedTodoStream)
	if len(sink.syncs) != 1 {
		t.Fatalf("an unchanged checklist must not be rewritten (%d writes)", len(sink.syncs))
	}
}

// Why: the conversation's plan always wins. SyncChecklist REPLACES the step
// set, so without this a delegated build would silently delete the checklist
// the boss's own brain laid out and put its own there instead. Once refused,
// it must stop asking rather than re-attempting the same refusal every poll
// for the next forty minutes.
func TestSyncNestedPlan_StandsDownWhenTheChatOwnsThePlan(t *testing.T) {
	sink := &recordingSink{err: plan.ErrPlanNotOwned}
	p := testPoll(sink)

	p.syncNestedPlan(context.Background(), nestedTodoStream)
	if !p.planDeclined {
		t.Fatal("a refusal must latch for the life of the job")
	}
	const changed = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t7","name":"TodoWrite","input":{"todos":[{"content":"Something else","status":"pending"}]}}]}}`
	p.syncNestedPlan(context.Background(), changed)
	if len(sink.syncs) != 1 {
		t.Fatalf("want no further attempts after standing down, got %d", len(sink.syncs))
	}
}

// Why: a transient database error is not a reason to stop mirroring for the
// rest of a forty-minute build, and it must never be recorded as success —
// otherwise the dock silently freezes on a stale checklist.
func TestSyncNestedPlan_RetriesAfterATransientFailure(t *testing.T) {
	sink := &recordingSink{err: errors.New("connection reset")}
	p := testPoll(sink)

	p.syncNestedPlan(context.Background(), nestedTodoStream)
	if p.planDeclined {
		t.Fatal("a transient failure must not latch the mirror off")
	}
	sink.err = nil
	p.syncNestedPlan(context.Background(), nestedTodoStream)
	if len(sink.syncs) != 2 {
		t.Fatalf("the same checklist must be retried after a failure, got %d writes", len(sink.syncs))
	}
}

// Why: a plan mirrored from a nested job must not outlive the job. Without the
// settle, a build that ended (or was killed) twenty minutes ago goes on drawing
// a live checklist above the composer — the stale-spinner shape of a false
// green this codebase exists to refuse.
func TestSettleNestedPlan_ClosesTheOwnedPlanOnAVerdict(t *testing.T) {
	sink := &recordingSink{}
	p := testPoll(sink)

	p.settleNestedPlan(context.Background(), false, "done")
	p.settleNestedPlan(context.Background(), true, "stopped")
	if len(sink.settles) != 2 {
		t.Fatalf("want both verdicts settled, got %d", len(sink.settles))
	}
	if sink.settles[0].runID != p.jobID || sink.settles[0].failed {
		t.Errorf("a success must settle this run's plan as done: %+v", sink.settles[0])
	}
	if !sink.settles[1].failed {
		t.Errorf("a failure must settle as failed: %+v", sink.settles[1])
	}
}

// Why: a job that could not book a run row falls back to a synthetic id
// ("job-<nanos>"). owner_run_id is a uuid column, so attempting either call
// would be a raw 22P02 on every poll for the life of the build.
func TestNestedPlan_NoRunRowMeansNoPlan(t *testing.T) {
	sink := &recordingSink{}
	p := testPoll(sink)
	p.jobID = "job-1724999999"

	p.syncNestedPlan(context.Background(), nestedTodoStream)
	p.settleNestedPlan(context.Background(), false, "done")
	if len(sink.syncs) != 0 || len(sink.settles) != 0 {
		t.Fatalf("a job with no run row owns no plan: %d syncs, %d settles", len(sink.syncs), len(sink.settles))
	}
}

// Why: an ephemeral sub-agent has no conversation to draw into and its id is
// not a session uuid, so a plan written for it would be an ownerless card on
// the boss's board that no conversation can ever resume.
func TestSyncNestedPlan_SkipsSessionsWithNoDock(t *testing.T) {
	for name, session := range map[string]string{
		"no session at all": "",
		"a synthetic id":    "delegate-child-7",
	} {
		sink := &recordingSink{}
		p := testPoll(sink)
		p.parentSession = session
		p.syncNestedPlan(context.Background(), nestedTodoStream)
		if len(sink.syncs) != 0 {
			t.Errorf("%s must not own a plan", name)
		}
	}
}
