package proactive

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/tools"
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

func TestIsForcePush(t *testing.T) {
	yes := []string{
		"git push --force",
		"git push -f origin main",
		"git push --force-with-lease",
		"git push origin +main",
		"git -C /repo push --force",
		"cd /x && git push -f",
		"git commit -m ok && git push --force origin main",
	}
	for _, c := range yes {
		if !isForcePush(c) {
			t.Errorf("isForcePush(%q) = false, want true", c)
		}
	}
	no := []string{
		"git push",
		"git push origin main",
		"git commit -m x && git push",
		"git status",
		"echo force push",
		"npm run push",
		"git pull --force", // pull, not push
	}
	for _, c := range no {
		if isForcePush(c) {
			t.Errorf("isForcePush(%q) = true, want false", c)
		}
	}
}

// Force-push must be blocked ONLY in autonomous runs; interactive (boss-driven)
// turns keep full git control. This pins the boss's explicit rule.
func TestForcePushBlockedOnlyWhenAutonomous(t *testing.T) {
	g := NewClaudeCodeGate(nil)
	in := map[string]any{"command": "git push --force origin main"}

	// Interactive (no autonomy marker): allowed.
	if dec := g.Authorize(context.Background(), "s", "p", "claude_code__bash", in); !dec.Allow {
		t.Errorf("interactive force-push should be ALLOWED, got blocked: %s", dec.Reason)
	}
	// Autonomous: blocked.
	actx := tools.WithAutonomous(context.Background())
	if dec := g.Authorize(actx, "s", "p", "claude_code__bash", in); dec.Allow {
		t.Error("autonomous force-push should be BLOCKED, got allowed")
	}
	// A normal autonomous push (no force) is NOT blocked by this rule.
	if dec := g.Authorize(actx, "s", "p", "claude_code__bash", map[string]any{"command": "git push origin main"}); !dec.Allow {
		t.Errorf("autonomous normal push should be allowed, got blocked: %s", dec.Reason)
	}
}

// The Approve/Deny card must LEAD with plain language. The boss's complaint,
// verbatim: approval requests were "very vague, not written in human language
// so I know what I'm accepting or declining" — because buildPreview was raw
// marshalled JSON. The human line comes first; the exact call stays below for
// anyone who wants it.
// It must read like a friend asking permission, in words anyone understands.
// The boss, verbatim: "WHAT THE FUCK IS BASH... I want to run bash because it
// will allow me to do X". So: what happens to his stuff (delete/send/download,
// never the tool name), whether it can be undone, and his own request as the
// why. Machinery stays below the fold.
func TestPreviewSpeaksHumanNotMachinery(t *testing.T) {
	ctx := agent.WithTurnIntent(context.Background(), "clean out my old builds")
	p := buildPreview(ctx, "claude_code__Bash", map[string]any{"command": "rm -rf ~/Dev/scratch/old-build"})

	// His own request leads the card — that IS the "why".
	if !strings.Contains(p, "You asked me to: “clean out my old builds”") {
		t.Fatalf("card must lead with the boss's own request, got:\n%s", p)
	}
	// The effect in plain words, not the mechanism.
	if !strings.Contains(p, "permanently delete") {
		t.Fatalf("card must say what happens in plain words, got:\n%s", p)
	}
	// The single most important fact on a permission card: can I take it back?
	if !strings.Contains(p, "can't be undone") {
		t.Fatalf("a destructive action must say it can't be undone, got:\n%s", p)
	}
	if !strings.Contains(p, "(on your Mac)") {
		t.Fatalf("card must name whose machine is touched, got:\n%s", p)
	}
	// "Bash" is machinery; above the fold it must not appear.
	human := strings.SplitN(p, "--- technical details ---", 2)[0]
	if strings.Contains(strings.ToLower(human), "bash") {
		t.Fatalf("the human half must not name the tool, got:\n%s", human)
	}
	if !strings.Contains(p, "--- technical details ---") {
		t.Fatalf("exact call must remain below the fold, got:\n%s", p)
	}
}

// Composio verbs translate structurally: GMAIL_FETCH_EMAILS reads as an action
// on his account, never as a verb id.
func TestPreviewTranslatesComposioVerbs(t *testing.T) {
	p := buildPreview(context.Background(), "composio__GMAIL_FETCH_EMAILS", map[string]any{"max_results": 5})
	human := strings.SplitN(p, "--- technical details ---", 2)[0]
	if !strings.Contains(human, "fetch emails (through your Gmail account)") {
		t.Fatalf("verb id must translate to plain words, got:\n%s", human)
	}
	if strings.Contains(human, "GMAIL_FETCH_EMAILS") {
		t.Fatalf("raw verb id must stay below the fold, got:\n%s", human)
	}
}

// No turn intent (autonomous cron fire) → the card still speaks human, just
// without the "You asked me to" line.
func TestPreviewWithoutIntentStillHuman(t *testing.T) {
	p := buildPreview(context.Background(), "claude_code__Bash", map[string]any{"command": "rm -rf /workspace/tmp"})
	if strings.Contains(p, "You asked me to") {
		t.Fatalf("no intent in ctx must mean no intent line, got:\n%s", p)
	}
	if !strings.HasPrefix(p, "I want to: permanently delete") {
		t.Fatalf("card must open with the plain effect, got:\n%s", p)
	}
}
