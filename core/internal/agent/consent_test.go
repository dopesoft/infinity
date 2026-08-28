package agent

import (
	"context"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/turnctx"
)

// Why: 2026-08-26, "Don't build.. lets discuss first" was followed by
// plan_create, plan_update and a code_agent spawn. The consent gate is the
// mechanic that makes "discuss" mean no work starts, regardless of what the
// soul, memory, or an injected directive nudges the model toward.

func TestIsConsentTool(t *testing.T) {
	work := []string{"project_create", "code_agent", "background_build", "document_create", "claude_code__Edit",
		"fs_save", "git_push", "delegate", "skills_invoke", "todo_write", "plan_update", "cron_create_agent",
		"sentinel_create", "composio__GMAIL_SEND_EMAIL", "composio__GOOGLECALENDAR_CREATE_EVENT"}
	for _, n := range work {
		if !isConsentTool(n) {
			t.Errorf("%s must need the boss's go while discussing", n)
		}
	}
	// plan_resume sits beside plan_approve here on purpose: they are the two
	// verbs by which the boss GRANTS consent, and gating the verb that grants
	// consent deadlocks the gate. That is the 2026-08-28 dead end — "please
	// continue the build" read as discussion, and the only way to say
	// "continue" was plan_update, which IS gated.
	talk := []string{"plan_create", "plan_revise", "plan_approve", "plan_resume", "plan_get", "recall", "remember", "web_search",
		"fs_read", "bash_run", "artifact_get", "load_tools", "tool_search", "composio__GMAIL_FETCH_EMAILS", "calendar_sync_now"}
	for _, n := range talk {
		if isConsentTool(n) {
			t.Errorf("%s must stay open while discussing", n)
		}
	}
}

func TestConsentBlocksOnlyWhenStanceIsDiscuss(t *testing.T) {
	// No holder (autonomous turn): never blocks.
	if hold, _ := consentBlocks(context.Background(), "code_agent"); hold {
		t.Fatal("autonomous turns must never be held")
	}
	// Discuss: held, with the classifier's reason.
	h := turnctx.NewStanceHolder()
	h.Set(turnctx.StanceDiscuss, "asked to talk first")
	ctx := turnctx.WithStance(context.Background(), h)
	hold, why := consentBlocks(ctx, "code_agent")
	if !hold || why != "asked to talk first" {
		t.Fatalf("discuss must hold work tools, got hold=%v why=%q", hold, why)
	}
	if hold, _ := consentBlocks(ctx, "plan_create"); hold {
		t.Fatal("plan_create (a proposal) must stay open while discussing")
	}
	// Work / unclear / unknown: open.
	for _, st := range []turnctx.Stance{turnctx.StanceWork, turnctx.StanceUnclear, turnctx.StanceUnknown} {
		h := turnctx.NewStanceHolder()
		h.Set(st, "")
		if hold, _ := consentBlocks(turnctx.WithStance(context.Background(), h), "code_agent"); hold {
			t.Fatalf("stance %q must not hold work tools", st)
		}
	}
}

func TestConsentWaitsForTheFirstReadingThenMovesOn(t *testing.T) {
	h := turnctx.NewStanceHolder()
	ctx := turnctx.WithStance(context.Background(), h)
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.Set(turnctx.StanceDiscuss, "late reading")
	}()
	start := time.Now()
	hold, _ := consentBlocks(ctx, "code_agent")
	if !hold {
		t.Fatal("the first work-tool call must wait for the classifier's reading")
	}
	if time.Since(start) > stanceWait {
		t.Fatal("waited past the bound")
	}
	// A steer flips it: "ok go ahead" mid-turn opens the tools.
	h.Set(turnctx.StanceWork, "boss said go")
	if hold, _ := consentBlocks(ctx, "code_agent"); hold {
		t.Fatal("a work steer must open the tools immediately")
	}
}

// TestDontBuildStillStopsWorkCold is the regression guard for the rule the
// resume path must NOT weaken. "Don't build, let's discuss first" has to stop
// work dead on a turn where the boss is genuinely talking — it is the reason
// this gate exists (2026-08-26). Adding a resume path for work he ALREADY
// approved must not open the gate generally.
func TestDontBuildStillStopsWorkCold(t *testing.T) {
	h := turnctx.NewStanceHolder()
	h.Set(turnctx.StanceDiscuss, "don't build, let's discuss first")
	ctx := turnctx.WithStance(context.Background(), h)

	for _, tool := range []string{"code_agent", "plan_update", "background_build", "claude_code__Edit", "git_push", "skills_invoke"} {
		hold, why := consentBlocks(ctx, tool)
		if !hold {
			t.Fatalf("%s ran while the boss said don't build", tool)
		}
		if why != "don't build, let's discuss first" {
			t.Fatalf("%s held for the wrong reason: %q", tool, why)
		}
	}
	// Nothing about being refused may latch the turn: a held turn is not a
	// working turn, so a later reading must still be free to move it either way.
	if latched, _ := h.Latched(); latched {
		t.Fatal("a turn whose work tools were all REFUSED must never be latched as working")
	}
	if !h.Set(turnctx.StanceDiscuss, "still talking") {
		t.Fatal("a discuss turn must stay freely re-classifiable")
	}
	// And the finishing reflexes stay off for the whole turn.
	if !turnIsDiscuss(ctx) {
		t.Fatal("self-heal / plan-continue / verify must stay off while he is talking")
	}
	if discussOverlayFor(ctx, true) == "" {
		t.Fatal("the conversation overlay must still be applied")
	}
}

// TestMidTurnSteerCannotDemoteAWorkingTurn is the Phase 6 mechanic: talking to
// Jarvis mid-build must not retroactively close the gate on work he approved.
// Before this, "how's it going?" classified as discuss, overwrote the turn's
// stance, and every remaining plan_update / code_agent call was refused.
func TestMidTurnSteerCannotDemoteAWorkingTurn(t *testing.T) {
	h := turnctx.NewStanceHolder()
	h.Set(turnctx.StanceWork, "boss said build it")
	ctx := turnctx.WithStance(context.Background(), h)

	// The build starts: a work tool actually runs, which latches the turn.
	if hold, _ := consentBlocks(ctx, "code_agent"); hold {
		t.Fatal("a work turn must run work tools")
	}
	if latched, why := h.Latched(); !latched || why != "code_agent" {
		t.Fatalf("running a work tool must latch the turn (latched=%v why=%q)", latched, why)
	}

	// Mid-build chat, classified as discussion.
	h.Set(turnctx.StanceDiscuss, "asking how it's going")

	if hold, _ := consentBlocks(ctx, "plan_update"); hold {
		t.Fatal("a chatty steer demoted an approved build: the rest of the plan is now blocked")
	}
	if turnIsDiscuss(ctx) {
		t.Fatal("a chatty steer switched off self-heal / plan-continue / verify mid-build")
	}
	if n, _ := h.RefusedDemotions(); n != 1 {
		t.Fatalf("the refused demotion must be recorded, got %d", n)
	}
}

// TestFreshTurnStartingAsDiscussIsUnaffected: the latch is per-turn, and a
// turn gets a fresh holder. Yesterday's build must not keep today's
// conversation open.
func TestFreshTurnStartingAsDiscussIsUnaffected(t *testing.T) {
	worked := turnctx.NewStanceHolder()
	worked.Set(turnctx.StanceWork, "build it")
	consentBlocks(turnctx.WithStance(context.Background(), worked), "code_agent")

	fresh := turnctx.NewStanceHolder()
	fresh.Set(turnctx.StanceDiscuss, "let's talk about it")
	if hold, _ := consentBlocks(turnctx.WithStance(context.Background(), fresh), "code_agent"); !hold {
		t.Fatal("a new turn that starts as a conversation must hold work tools")
	}
}

// TestSteerCanStillDemoteBeforeAnyWorkRan: the latch only closes once work has
// actually happened. A turn that read as work but has not run anything yet
// ("actually, hold on — let's talk about it first") must still be stoppable.
func TestSteerCanStillDemoteBeforeAnyWorkRan(t *testing.T) {
	h := turnctx.NewStanceHolder()
	h.Set(turnctx.StanceWork, "sounded like a work order")
	ctx := turnctx.WithStance(context.Background(), h)

	// No work tool has run. He changes his mind.
	if !h.Set(turnctx.StanceDiscuss, "hold on, let's talk first") {
		t.Fatal("a turn that has not run anything must still be demotable")
	}
	if hold, _ := consentBlocks(ctx, "code_agent"); !hold {
		t.Fatal("after he says hold on, work tools must be held")
	}
}

func TestDiscussRefusalTellsTheModelToPropose(t *testing.T) {
	msg := discussRefusal("project_create", "asked to discuss")
	for _, want := range []string{"HOLD", "project_create", "plan_create", "PROPOSAL", "Do not retry"} {
		if !contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
