package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/tools"
)

func TestReferencesCloudWorkspace(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/workspace/artifacts/crm-automation-report.md", true},
		{"/workspace", true},
		{"ls -l /workspace/artifacts/x.pdf", true},
		{"cat \"/workspace/notes.md\"", true},
		{"cd /workspace; ls", true},
		{"/Users/n0m4d/Dev/infinity/core", false},
		{"~/Dev/report.md", false},
		{"/workspaces/foo", false}, // plural — different dir, must NOT match
		{"/workspacexyz", false},
		{"", false},
	}
	for _, c := range cases {
		if got := referencesCloudWorkspace(c.in); got != c.want {
			t.Errorf("referencesCloudWorkspace(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCloudPathRedirectGate(t *testing.T) {
	g := CloudPathRedirectGate{}
	ctx := context.Background()

	// claude_code touching /workspace → redirected (not allowed, no approval).
	d := g.Authorize(ctx, "s", "", "claude_code__Read", map[string]any{"file_path": "/workspace/artifacts/x.md"})
	if d.Allow || d.WaitForApproval {
		t.Fatalf("claude_code__Read on /workspace should be redirected, got %+v", d)
	}
	if !strings.Contains(d.Reason, "bash_run") {
		t.Fatalf("redirect reason should steer to bash_run, got %q", d.Reason)
	}

	// claude_code Bash with a /workspace ls → redirected.
	d = g.Authorize(ctx, "s", "", "claude_code__Bash", map[string]any{"command": "ls -l /workspace/artifacts"})
	if d.Allow {
		t.Fatalf("claude_code__Bash ls /workspace should be redirected, got %+v", d)
	}

	// claude_code on a Mac path → allowed (passes through to the next gate).
	d = g.Authorize(ctx, "s", "", "claude_code__Read", map[string]any{"file_path": "/Users/n0m4d/Dev/infinity/README.md"})
	if !d.Allow {
		t.Fatalf("claude_code__Read on a Mac path should be allowed, got %+v", d)
	}

	// A non-claude_code tool → always allowed here.
	d = g.Authorize(ctx, "s", "", "bash_run", map[string]any{"command": "ls /workspace"})
	if !d.Allow {
		t.Fatalf("bash_run (a cloud tool) must be allowed even on /workspace, got %+v", d)
	}
}

// A redirect is a CORRECTION to the agent, not a question for the boss. The
// 2026-07-09 regression: the gate correctly refused claude_code__Bash on a
// /workspace yt-dlp command, but formatGatedOutput rendered every Allow=false
// as "requires the boss's approval" and — since a redirect carries no Trust
// contract by design — appended "the Trust store is misconfigured and the
// action was simply refused". Jarvis obeyed that literally, reported a broken
// bridge, and never called bash_run. The decision was right; the words the
// model read were wrong. This asserts the words.
func TestRedirectOutputNeverAsksForApproval(t *testing.T) {
	d := CloudPathRedirectGate{}.Authorize(
		context.Background(), "s", "", "claude_code__Bash",
		map[string]any{"command": "source /workspace/.jarvis/env.sh && yt-dlp --skip-download --write-auto-subs https://youtu.be/x"},
	)
	if !d.Redirect {
		t.Fatalf("a wrong-bridge block must be flagged Redirect, got %+v", d)
	}
	out := formatGatedOutput("claude_code__Bash", d)

	// Nothing in a redirect may push the model toward the boss or toward
	// declaring failure — those are what made it give up instead of retrying.
	for _, forbidden := range []string{"approval", "Trust", "misconfigured", "queued"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("redirect output must not mention %q; got:\n%s", forbidden, out)
		}
	}
	// And it must hand the model the tool it should have called.
	if !strings.Contains(out, "bash_run") {
		t.Fatalf("redirect output must name bash_run; got:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "retry now") {
		t.Fatalf("redirect output must tell the model to retry; got:\n%s", out)
	}
}

// An ordinary Trust gate must keep its approval framing — the redirect branch
// must not swallow the case it was carved out of.
func TestApprovalOutputStillAsksForApproval(t *testing.T) {
	out := formatGatedOutput("claude_code__Bash", GateDecision{
		Allow: false, Reason: "destructive", ContractID: "abc-123", WaitForApproval: true,
	})
	if !strings.Contains(out, "approval") || !strings.Contains(out, "abc-123") {
		t.Fatalf("approval gate must still ask for approval and cite its contract; got:\n%s", out)
	}
}

// The hole the /workspace check could never see: `yt-dlp <url>` names no cloud
// path, so it sails past the path gate and executes on the Mac — where the
// binary does not exist. The catalog is what makes this call knowable.
func TestGateRedirectsBareCloudCLIOnMacBridge(t *testing.T) {
	tools.AttachCLICatalog(stubCatalog{})
	t.Cleanup(func() { tools.AttachCLICatalog(nil) })

	d := CloudPathRedirectGate{}.Authorize(
		context.Background(), "s", "", "claude_code__Bash",
		map[string]any{"command": "yt-dlp --skip-download --write-auto-subs https://youtu.be/abc"},
	)
	if d.Allow {
		t.Fatal("a cloud-resident binary must never run on the Mac bridge")
	}
	if !d.Redirect {
		t.Fatalf("must be a redirect, not an approval gate: %+v", d)
	}
	if !strings.Contains(d.Reason, "bash_run") {
		t.Fatalf("must steer to bash_run, got %q", d.Reason)
	}

	// A Mac command that merely mentions the tool is still the boss's to run.
	d = CloudPathRedirectGate{}.Authorize(
		context.Background(), "s", "", "claude_code__Bash",
		map[string]any{"command": `git commit -m "bump yt-dlp"`},
	)
	if !d.Allow {
		t.Fatalf("a git commit mentioning yt-dlp must run on the Mac, got %+v", d)
	}
}

type stubCatalog struct{}

func (stubCatalog) CloudCLIBinaries(context.Context) []string { return []string{"yt-dlp", "ffmpeg"} }
func (stubCatalog) CloudEnvPrelude() string                   { return "source /workspace/.jarvis/env.sh && " }
