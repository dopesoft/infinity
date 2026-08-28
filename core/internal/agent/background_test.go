package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/tools"
)

func TestBackgroundLabelUsesBridgeWorkerPrefix(t *testing.T) {
	cloud := backgroundLabel("Fix the worker label", string(bridge.KindCloud))
	if want := "Cloud agent: Fix the worker label"; cloud != want {
		t.Fatalf("cloud label = %q, want %q", cloud, want)
	}

	mac := backgroundLabel("Fix the worker label", string(bridge.KindMac))
	if want := "Mac agent: Fix the worker label"; mac != want {
		t.Fatalf("mac label = %q, want %q", mac, want)
	}
}

func TestBackgroundLabelFallsBackForEmptyTask(t *testing.T) {
	cloud := backgroundLabel("", string(bridge.KindCloud))
	if want := "Cloud agent: background build"; cloud != want {
		t.Fatalf("cloud empty label = %q, want %q", cloud, want)
	}

	unknown := backgroundLabel("", "")
	if want := "background build"; unknown != want {
		t.Fatalf("unknown empty label = %q, want %q", unknown, want)
	}
}

func TestBackgroundWorkerAndBackendLabels(t *testing.T) {
	if got := backgroundWorkerLabel(string(bridge.KindCloud)); got != "Cloud agent" {
		t.Fatalf("cloud worker label = %q", got)
	}
	if got := backgroundBackendLabel(string(bridge.KindCloud)); got != "settings model" {
		t.Fatalf("cloud backend label = %q", got)
	}
	if got := backgroundWorkerLabel(string(bridge.KindMac)); got != "Mac agent" {
		t.Fatalf("mac worker label = %q", got)
	}
	if got := backgroundBackendLabel(string(bridge.KindMac)); got != "settings model" {
		t.Fatalf("mac backend label = %q", got)
	}
}

func TestBackgroundAgentActiveBridgeKind(t *testing.T) {
	a := &BackgroundAgent{Bridge: func(context.Context, string) bridge.Preference { return bridge.PrefCloud }}
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != string(bridge.KindCloud) {
		t.Fatalf("cloud preference picked %q", got)
	}

	a.Bridge = func(context.Context, string) bridge.Preference { return bridge.PrefMac }
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != string(bridge.KindMac) {
		t.Fatalf("mac preference picked %q", got)
	}

	a.Bridge = nil
	if got := a.activeBridgeKind(context.Background(), "chat-1"); got != "" {
		t.Fatalf("nil bridge picked %q", got)
	}
}

// ── Mac bridge → Claude Code ─────────────────────────────────────────────

type fakeMacBridge struct{}

func (fakeMacBridge) Name() bridge.Kind                               { return bridge.KindMac }
func (fakeMacBridge) BaseURL() string                                 { return "http://mac.test" }
func (fakeMacBridge) Health(context.Context) bool                     { return true }
func (fakeMacBridge) Get(context.Context, string) ([]byte, int, bool) { return nil, 200, true }
func (fakeMacBridge) Post(context.Context, string, any) ([]byte, int, bool) {
	return nil, 200, true
}

type fakeCodeRunner struct {
	ran *tools.ClaudeCodeJob
}

func (f *fakeCodeRunner) ActiveBridge(context.Context) (bridge.Bridge, string, error) {
	return fakeMacBridge{}, "test", nil
}
func (f *fakeCodeRunner) DefaultModel() string  { return "claude-opus-5[1m]" }
func (f *fakeCodeRunner) DefaultEffort() string { return "high" }
func (f *fakeCodeRunner) Run(_ context.Context, job tools.ClaudeCodeJob) (string, error) {
	f.ran = &job
	job.SetMeta("auth", "Max subscription · kai@example.com")
	job.Heartbeat("Claude Code · Edit core/x.go · 5s", "Edit", "core/x.go")
	job.Heartbeat("Claude Code · Edit core/x.go · 20s", "Edit", "core/x.go")
	job.Heartbeat("Claude Code · Bash go test ./... · 35s", "Bash", "go test ./...")
	return "Done. Build is clean.", nil
}

// Why: the boss's contract (2026-08-28): on the Mac bridge CODING runs on his
// Claude Max subscription, the ChatGPT brain is for conversation. Before
// this, background_build spun a full ChatGPT-billed coding loop on the Mac
// (13 minutes on 2026-08-27, again 2026-08-28) and spent his plan. The Mac
// path must hand the task to Claude Code and never touch the settings model.
func TestBackgroundBuildOnMacRunsClaudeCode(t *testing.T) {
	runner := &fakeCodeRunner{}
	var progress []BackgroundProgress
	a := &BackgroundAgent{
		// Loop deliberately nil: the Mac path must never reach the settings-model loop.
		Code: runner,
		OnProgress: func(_ context.Context, p BackgroundProgress) {
			progress = append(progress, p)
		},
	}
	summary, err := a.runToCompletion(context.Background(), "chat-1", "run-1",
		"Finish the feature", "Files live under core/internal/pursuits", "/Users/kai/Dev/infinity",
		nil, nil, string(bridge.KindMac), fakeMacBridge{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary != "Done. Build is clean." {
		t.Fatalf("summary = %q", summary)
	}
	if runner.ran == nil {
		t.Fatal("the task must have been handed to Claude Code")
	}
	job := runner.ran
	if job.Model != "claude-opus-5[1m]" || job.Effort != "high" {
		t.Fatalf("the pinned Claude model/effort must apply to background builds too: %q %q", job.Model, job.Effort)
	}
	if job.Repo != "/Users/kai/Dev/infinity" {
		t.Fatalf("repo must reach the launch (Claude reads CLAUDE.md from there): %q", job.Repo)
	}
	if job.KillOnCancel {
		t.Fatal("a background build must not be guillotined at its deadline (2026-08-27: three half-done runs were)")
	}
	if !strings.Contains(job.Task, "Finish the feature") || !strings.Contains(job.Task, "## Brief") {
		t.Fatalf("task + brief must both reach Claude: %q", job.Task)
	}
	// Live progress reaches the parent chat with the tool + target, and the
	// step count advances per distinct tool call, not per poll.
	var last BackgroundProgress
	for _, p := range progress {
		last = p
	}
	if last.Action != "Bash" || last.Detail != "go test ./..." || last.Step != 2 {
		t.Fatalf("progress must name Claude's current tool and count distinct calls: %+v", last)
	}
	for _, p := range progress {
		if p.ParentSession != "chat-1" || p.RunID != "run-1" {
			t.Fatalf("progress must bind to the boss's conversation + run: %+v", p)
		}
	}
}

func TestInferRepoPath(t *testing.T) {
	cases := map[string]string{
		"Work in /Users/kai/Dev/infinity and finish the pursuit.": "/Users/kai/Dev/infinity",
		"repo ~/Dev/ELMAGO, run tests":                            "~/Dev/ELMAGO",
		"cloud: /workspace/infinity/core":                         "/workspace/infinity",
		"no path here":                                            "",
	}
	for in, want := range cases {
		if got := inferRepoPath(in); got != want {
			t.Fatalf("inferRepoPath(%q) = %q, want %q", in, got, want)
		}
	}
}
