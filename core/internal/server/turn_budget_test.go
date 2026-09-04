package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// A budget that looks every few milliseconds, so the tests run in tens of ms.
func fastBudget(stall, ceiling time.Duration) turnBudget {
	return turnBudget{stall: stall, ceiling: ceiling, tick: 2 * time.Millisecond}
}

func thinkingJournal() *turnJournal {
	j := newTurnJournal("s")
	j.begin("turn-1", "deepseek")
	j.setPhase(agent.RunEvent{Kind: agent.EventThinking, ThinkingDelta: "…"})
	return j
}

func waitDone(t *testing.T, ctx context.Context, within time.Duration) bool {
	t.Helper()
	select {
	case <-ctx.Done():
		return true
	case <-time.After(within):
		return false
	}
}

// THE BUG THIS REPLACES: a flat 15-minute wall clock cut off a research turn
// that was producing frames the whole time. A turn that keeps showing signs
// of life must never be cut by the stall budget, however long it runs.
func TestGuardTurn_ATurnThatKeepsTalkingIsNeverStalled(t *testing.T) {
	j := thinkingJournal()
	ctx, cancel := guardTurn(context.Background(), j, fastBudget(20*time.Millisecond, time.Hour))
	defer cancel()
	deadline := time.Now().Add(120 * time.Millisecond)
	for time.Now().Before(deadline) {
		j.append(wsServerEvent{Type: "thinking", Text: "…"})
		time.Sleep(5 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatalf("a turn producing a frame every 5ms was cancelled: cause=%v", context.Cause(ctx))
	}
}

// Silence while the brain should be talking is a wedged provider, and the
// turn ends with the STALL cause so the loop can say so.
func TestGuardTurn_SilenceWhileThinkingStallsWithItsCause(t *testing.T) {
	j := thinkingJournal()
	ctx, cancel := guardTurn(context.Background(), j, fastBudget(20*time.Millisecond, time.Hour))
	defer cancel()
	if !waitDone(t, ctx, 500*time.Millisecond) {
		t.Fatal("a brain silent past the stall budget was never cut")
	}
	if !errors.Is(context.Cause(ctx), agent.ErrTurnStalled) {
		t.Fatalf("cause = %v, want ErrTurnStalled", context.Cause(ctx))
	}
	stalled, ceiling := agent.TurnBudgetCause(ctx)
	if !stalled || ceiling {
		t.Fatalf("TurnBudgetCause = (%v, %v), want (true, false)", stalled, ceiling)
	}
}

// A tool running for twenty minutes is silent by nature, and so is an
// approval the boss has not decided yet. Neither is a stall.
func TestGuardTurn_AQuietToolOrAParkedApprovalIsNotAStall(t *testing.T) {
	for _, ev := range []agent.RunEvent{
		{Kind: agent.EventToolCall, ToolCall: &agent.ToolEvent{Name: "bash_run"}},
		{Kind: agent.EventToolCall, ToolCall: &agent.ToolEvent{Name: "bash_run", AwaitingApproval: true}},
	} {
		j := thinkingJournal()
		j.setPhase(ev)
		ctx, cancel := guardTurn(context.Background(), j, fastBudget(10*time.Millisecond, time.Hour))
		if waitDone(t, ctx, 80*time.Millisecond) {
			t.Fatalf("phase %q was cut for silence: cause=%v", j.activity().phase, context.Cause(ctx))
		}
		cancel()
	}
}

// The ceiling is absolute: it applies even while a tool runs, with its own
// cause so the note reads "time budget", not "went quiet".
func TestGuardTurn_TheCeilingAppliesEvenMidTool(t *testing.T) {
	j := thinkingJournal()
	j.setPhase(agent.RunEvent{Kind: agent.EventToolCall, ToolCall: &agent.ToolEvent{Name: "code_agent"}})
	ctx, cancel := guardTurn(context.Background(), j, fastBudget(time.Hour, 20*time.Millisecond))
	defer cancel()
	if !waitDone(t, ctx, 500*time.Millisecond) {
		t.Fatal("a turn past its ceiling was never cut")
	}
	if !errors.Is(context.Cause(ctx), agent.ErrTurnCeiling) {
		t.Fatalf("cause = %v, want ErrTurnCeiling", context.Cause(ctx))
	}
	stalled, ceiling := agent.TurnBudgetCause(ctx)
	if stalled || !ceiling {
		t.Fatalf("TurnBudgetCause = (%v, %v), want (false, true)", stalled, ceiling)
	}
}

// The Stop button is a plain cancel: no budget cause, so the loop files it as
// interrupted by the boss, never as a stall.
func TestGuardTurn_StopIsNotABudgetCause(t *testing.T) {
	j := thinkingJournal()
	ctx, cancel := guardTurn(context.Background(), j, fastBudget(time.Hour, time.Hour))
	cancel()
	if !waitDone(t, ctx, 100*time.Millisecond) {
		t.Fatal("cancel did not end the context")
	}
	if stalled, ceiling := agent.TurnBudgetCause(ctx); stalled || ceiling {
		t.Fatalf("a plain cancel reported a budget cause (%v, %v)", stalled, ceiling)
	}
}

// Once the turn is over the guard stands down: a finished journal must not
// cancel anything, however quiet it goes.
func TestGuardTurn_StandsDownWhenTheTurnEnds(t *testing.T) {
	j := thinkingJournal()
	ctx, cancel := guardTurn(context.Background(), j, fastBudget(10*time.Millisecond, time.Hour))
	defer cancel()
	j.end("end_turn")
	if waitDone(t, ctx, 80*time.Millisecond) {
		t.Fatalf("a finished turn was cancelled: cause=%v", context.Cause(ctx))
	}
}

// The defaults are the documented ones, and the env knobs override them.
func TestTurnBudgetFromEnv(t *testing.T) {
	t.Setenv("INFINITY_TURN_STALL", "")
	t.Setenv("INFINITY_TURN_TIMEOUT", "")
	b := turnBudgetFromEnv()
	if b.stall != defaultTurnStall || b.ceiling != defaultTurnCeiling {
		t.Fatalf("defaults = %v/%v", b.stall, b.ceiling)
	}
	t.Setenv("INFINITY_TURN_STALL", "3m")
	t.Setenv("INFINITY_TURN_TIMEOUT", "45m")
	b = turnBudgetFromEnv()
	if b.stall != 3*time.Minute || b.ceiling != 45*time.Minute {
		t.Fatalf("env override = %v/%v", b.stall, b.ceiling)
	}
	t.Setenv("INFINITY_TURN_STALL", "not a duration")
	if b := turnBudgetFromEnv(); b.stall != defaultTurnStall {
		t.Fatalf("a bad value must fall back to the default, got %v", b.stall)
	}
}
