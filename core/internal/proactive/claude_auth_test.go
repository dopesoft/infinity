package proactive

import (
	"context"
	"strings"
	"testing"
	"time"
)

func read(st ClaudeAuthState) func(context.Context) (ClaudeAuthState, error) {
	return func(context.Context) (ClaudeAuthState, error) { return st, nil }
}

func run(t *testing.T, st ClaudeAuthState) []Finding {
	t.Helper()
	f, err := ClaudeAuthChecklist(read(st))(context.Background(), nil)
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}
	return f
}

// The whole point of this checklist is to speak BEFORE the token dies, since
// it dies a year out, unannounced, most likely during an overnight build.
func TestClaudeAuthWarnsBeforeTheTokenExpires(t *testing.T) {
	month11 := ClaudeAuthState{Configured: true, MacReady: true, CloudReady: true, TokenAge: 340 * 24 * time.Hour}
	got := run(t, month11)
	if len(got) != 1 || got[0].SourceTag != "claude_auth:expiring" {
		t.Fatalf("want an expiring warning, got %+v", got)
	}

	// Comfortably inside the year: nothing to say. A checklist that talks on
	// every tick is one he learns to ignore.
	quiet := run(t, ClaudeAuthState{Configured: true, MacReady: true, CloudReady: true, TokenAge: 30 * 24 * time.Hour})
	if len(quiet) != 0 {
		t.Fatalf("a healthy token should be silent, got %+v", quiet)
	}

	past := run(t, ClaudeAuthState{Configured: true, MacReady: true, CloudReady: true, TokenAge: 400 * 24 * time.Hour})
	if len(past) != 1 || past[0].SourceTag != "claude_auth:expired" {
		t.Fatalf("want an expired warning, got %+v", past)
	}
}

// A token saved before Infinity started recording the date has an unknown
// age. Guessing an expiry and being wrong is worse than staying quiet.
func TestClaudeAuthSilentWhenAgeIsUnknown(t *testing.T) {
	got := run(t, ClaudeAuthState{Configured: true, MacReady: true, CloudReady: true, TokenAge: 0})
	if len(got) != 0 {
		t.Fatalf("unknown age should produce no age warning, got %+v", got)
	}
}

// Working-but-only-while-the-laptop-is-open is worth saying once, because the
// failure it predicts lands overnight when he cannot fix it.
func TestClaudeAuthFlagsTheOvernightGap(t *testing.T) {
	got := run(t, ClaudeAuthState{Configured: true, MacReady: true})
	if len(got) != 1 || got[0].SourceTag != "claude_auth:cloud_missing" {
		t.Fatalf("want the overnight-gap warning, got %+v", got)
	}
	if !strings.Contains(got[0].Detail, "setup-token") {
		t.Error("the warning must name the fix, not just the problem")
	}
}

// Neither machine can sign in: the loudest case, and the only one where
// nothing on the subscription can run at all.
func TestClaudeAuthFlagsTotalLoss(t *testing.T) {
	got := run(t, ClaudeAuthState{Configured: true})
	if len(got) != 1 || got[0].SourceTag != "claude_auth:none" {
		t.Fatalf("want the total-loss warning, got %+v", got)
	}
}

// Not wired on this deploy means there is nothing to warn about.
func TestClaudeAuthSilentWhenNotConfigured(t *testing.T) {
	if got := run(t, ClaudeAuthState{}); len(got) != 0 {
		t.Fatalf("want silence, got %+v", got)
	}
}
