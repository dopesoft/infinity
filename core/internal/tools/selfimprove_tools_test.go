package tools

import (
	"context"
	"errors"
	"testing"
)

// stubDecider is a no-op CodeProposalDecider for tests that don't care about
// persistence — they only test the gate logic that runs before DecideCodeProposal.
type stubDecider struct{}

func (s *stubDecider) DecideCodeProposal(_ context.Context, _, _, _ string) error { return nil }

// makeDeploySnapshot returns a fake deploy snapshot as a map[string]any in the
// same shape as server.deployStatus JSON (running_sha, latest_sha).
func makeDeploySnapshot(running, latest string) func() any {
	return func() any {
		return map[string]any{
			"running_sha": running,
			"latest_sha":  latest,
		}
	}
}

// TestCheckDeployedBeforeApplied covers the pure-function gate directly.
func TestCheckDeployedBeforeApplied(t *testing.T) {
	cases := []struct {
		name        string
		running     string
		latest      string
		expectBlock bool
	}{
		{"shas match — deploy landed", "abc12345", "abc12345", false},
		{"shas differ — deploy not landed", "oldsha1", "newsha1", true},
		{"running empty — uninitialised env", "", "abc12345", false},
		{"latest empty — no push yet", "abc12345", "", false},
		{"both empty — local dev", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := map[string]any{
				"running_sha": c.running,
				"latest_sha":  c.latest,
			}
			err := checkDeployedBeforeApplied(snap)
			if c.expectBlock && err == nil {
				t.Fatalf("expected error (blocked), got nil (running=%q latest=%q)", c.running, c.latest)
			}
			if !c.expectBlock && err != nil {
				t.Fatalf("expected nil (allowed), got %v (running=%q latest=%q)", err, c.running, c.latest)
			}
		})
	}
}

// TestCodeProposalDecide_AppliedGate verifies the tool wires the deploy gate
// correctly: "applied" is blocked in autonomous mode when the deploy is behind,
// allowed once the deploy catches up; "approved" and "rejected" are never
// affected regardless of deploy state.
func TestCodeProposalDecide_AppliedGate(t *testing.T) {
	bgCtx := context.Background()
	autonomousCtx := WithAutonomous(bgCtx)

	behindDeploy := makeDeploySnapshot("oldhash1", "newhash2")
	currentDeploy := makeDeploySnapshot("abc12345", "abc12345")
	noDeploy := makeDeploySnapshot("", "")

	cases := []struct {
		name        string
		ctx         context.Context
		decision    string
		deployFn    func() any
		expectError bool
	}{
		// "applied" in autonomous mode with deploy behind → blocked
		{"applied autonomous behind", autonomousCtx, "applied", behindDeploy, true},
		// "applied" in autonomous mode with deploy current → allowed
		{"applied autonomous current", autonomousCtx, "applied", currentDeploy, false},
		// "applied" in autonomous mode with no Railway env → allowed (local dev)
		{"applied autonomous no-env", autonomousCtx, "applied", noDeploy, false},
		// "applied" in autonomous mode with nil deployFn → allowed (gate off)
		{"applied autonomous nil fn", autonomousCtx, "applied", nil, false},
		// "applied" in interactive mode (boss-driven) → always allowed regardless of SHA
		{"applied interactive behind", bgCtx, "applied", behindDeploy, false},
		// "approved" in autonomous mode with deploy behind → never blocked by deploy gate
		{"approved autonomous behind", autonomousCtx, "approved", behindDeploy, false},
		// "rejected" in autonomous mode with deploy behind → never blocked by deploy gate
		{"rejected autonomous behind", autonomousCtx, "rejected", behindDeploy, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool := &codeProposalDecide{
				decider:  &stubDecider{},
				deployFn: c.deployFn,
			}
			_, err := tool.Execute(c.ctx, map[string]any{
				"id":       "test-id",
				"decision": c.decision,
			})
			if c.expectError && err == nil {
				t.Fatalf("expected error (gate should block), got nil")
			}
			if !c.expectError && err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
		})
	}
}

// TestCodeProposalDecide_InvalidDecision verifies unchanged validation —
// unknown decisions are still rejected regardless of deploy state.
func TestCodeProposalDecide_InvalidDecision(t *testing.T) {
	tool := &codeProposalDecide{decider: &stubDecider{}}
	_, err := tool.Execute(context.Background(), map[string]any{
		"id":       "test-id",
		"decision": "completed", // not a valid value
	})
	if err == nil {
		t.Fatal("expected error for invalid decision, got nil")
	}
	_ = errors.Is(err, errors.New("")) // just ensures errors package is used
}
