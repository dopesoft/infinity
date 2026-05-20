package proactive

import (
	"context"
	"testing"
)

// bashInput is a small helper mirroring the claude_code__bash tool input shape.
func bashInput(cmd string) map[string]any { return map[string]any{"command": cmd} }

// TestIsDestructiveBash encodes the boss's intent: writing/building code must
// flow freely, only filesystem-destructive shell commands should stop. Each
// case states WHY it matters so the table can't silently rot if the policy
// boundary moves.
func TestIsDestructiveBash(t *testing.T) {
	g := NewClaudeCodeGate(nil)

	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		// Safe — these are the bread-and-butter of writing code and must never gate.
		{"go build", "go build ./...", false},
		{"go test", "go test ./core/...", false},
		{"npm test", "npm test", false},
		{"mkdir", "mkdir -p core/internal/foo", false},
		{"git add+commit", "git add -A && git commit -m 'wip'", false},
		{"cat read", "cat core/internal/proactive/gate.go", false},
		{"grep", "grep -rn rm core/", false}, // "rm" appears as an arg, not the command
		{"echo redirect (overwrite is git-reversible)", "echo 'package main' > main.go", false},
		{"ls", "ls -la", false},

		// Destructive — these can delete/destroy files and must stop for approval.
		{"rm -rf", "rm -rf build/", true},
		{"rm single", "rm core/foo.go", true},
		{"rmdir", "rmdir tmp", true},
		{"shred", "shred -u secret.txt", true},
		{"dd", "dd if=/dev/zero of=/dev/sda", true},
		{"mkfs", "mkfs.ext4 /dev/sdb1", true}, // exe token is mkfs.ext4 -> not exact; see note
		{"truncate", "truncate -s 0 important.log", true},
		{"find -delete", "find . -name '*.tmp' -delete", true},
		{"find -exec rm", "find . -name '*.log' -exec rm {} +", true},
		{"mv to devnull", "mv secrets.env /dev/null", true},

		// Hidden behind chains / wrappers — still destructive, must be caught.
		{"chained rm", "go build ./... && rm -rf dist", true},
		{"piped xargs rm", "find . -name '*.bak' | xargs rm", true},
		{"sudo rm", "sudo rm -rf /var/cache/foo", true},
		{"subshell rm", "echo $(rm -rf x)", true},
		{"env-prefixed rm", "FORCE=1 rm -rf node_modules", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := g.isDestructiveBash(bashInput(tc.cmd))
			if got != tc.want {
				t.Fatalf("isDestructiveBash(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

// TestAuthorizeBashAndEdits verifies the end-to-end gate decision: edits and
// safe bash get Allow=true (no approval), destructive bash does NOT get
// Allow=true (it routes to the queue path — here with a nil trust store it
// fails closed, which still proves it left the allow path).
func TestAuthorizeBashAndEdits(t *testing.T) {
	g := NewClaudeCodeGate(nil)
	ctx := context.Background()

	allow := func(tool string, in map[string]any) bool {
		return g.Authorize(ctx, "sess-1", "infinity", tool, in).Allow
	}

	// Source edits flow freely — this is the whole point.
	if !allow("claude_code__write", map[string]any{"file_path": "x.go", "content": "package x"}) {
		t.Fatal("claude_code__write should be allowed without approval")
	}
	if !allow("claude_code__edit", map[string]any{"file_path": "x.go"}) {
		t.Fatal("claude_code__edit should be allowed without approval")
	}

	// Safe bash flows freely.
	if !allow("claude_code__bash", bashInput("go build ./...")) {
		t.Fatal("safe bash (go build) should be allowed without approval")
	}
	if !allow("claude_code__bash", bashInput("git status")) {
		t.Fatal("read-only git should be allowed without approval")
	}

	// Destructive bash must NOT be auto-allowed.
	if allow("claude_code__bash", bashInput("rm -rf build/")) {
		t.Fatal("destructive bash (rm -rf) must not be auto-allowed")
	}

	// Non-claude_code tools are untouched.
	if !allow("memory_search", map[string]any{"q": "anything"}) {
		t.Fatal("non-claude_code tools must pass through")
	}
}

// TestBridgeGateAuthorize mirrors the claude_code policy on the generic bridge
// surface: fs edits + safe bash_run flow, destructive bash_run + git_push stop.
// This is the surface that actually generated the bulk of the boss's prompt
// pile (read-only greps/seds/cats that should never have gated).
func TestBridgeGateAuthorize(t *testing.T) {
	g := NewBridgeGate(nil)
	ctx := context.Background()

	allow := func(tool string, in map[string]any) bool {
		return g.Authorize(ctx, "sess-1", "infinity", tool, in).Allow
	}
	bashRun := func(cmd string) map[string]any { return map[string]any{"cmd": cmd} }

	// Writing code flows freely.
	if !allow("fs_save", map[string]any{"path": "x.go"}) {
		t.Fatal("fs_save should be allowed without approval")
	}
	if !allow("fs_edit", map[string]any{"path": "x.go"}) {
		t.Fatal("fs_edit should be allowed without approval")
	}
	if !allow("git_commit", map[string]any{"message": "wip"}) {
		t.Fatal("git_commit should be allowed without approval")
	}

	// The bulk of the real pile: read-only exploration via bash_run.
	for _, c := range []string{
		"grep -RIn web_search core/", "sed -n '1,220p' serve.go",
		"cat go.mod", "pwd && ls", "env | grep LLM", "go build ./...",
	} {
		if !allow("bash_run", bashRun(c)) {
			t.Fatalf("safe bash_run %q should be allowed without approval", c)
		}
	}

	// Destructive bash_run still stops.
	if allow("bash_run", bashRun("rm -rf /workspace/infinity/dist")) {
		t.Fatal("destructive bash_run (rm -rf) must not be auto-allowed")
	}
	// Outward-facing publish still stops.
	if allow("git_push", map[string]any{"remote": "origin", "branch": "main"}) {
		t.Fatal("git_push must not be auto-allowed")
	}
	// Non-bridge tools pass through.
	if !allow("memory_search", map[string]any{"q": "x"}) {
		t.Fatal("non-bridge tools must pass through")
	}
}

// TestAuthorizeGateAllBash verifies the legacy escape hatch: with gateAllBash
// set, even a harmless build command leaves the allow path.
func TestAuthorizeGateAllBash(t *testing.T) {
	g := NewClaudeCodeGate(nil)
	g.gateAllBash = true
	ctx := context.Background()

	// Read-only git still allowed even in gate-all mode.
	if !g.Authorize(ctx, "s", "p", "claude_code__bash", bashInput("git diff")).Allow {
		t.Fatal("read-only git should stay allowed in gate-all mode")
	}
	// Otherwise every bash gates.
	if g.Authorize(ctx, "s", "p", "claude_code__bash", bashInput("go build ./...")).Allow {
		t.Fatal("gate-all mode must gate even safe bash")
	}
}
