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
	talk := []string{"plan_create", "plan_revise", "plan_approve", "plan_get", "recall", "remember", "web_search",
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
