package agent

import (
	"context"
	"strings"
	"testing"
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
