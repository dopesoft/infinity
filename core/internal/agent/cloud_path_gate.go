package agent

import (
	"context"
	"strings"
	"time"
)

// CloudPathRedirectGate stops the wrong-bridge thrash: the model produces a
// deliverable on the CLOUD workspace (document_create / media_job write to
// /workspace/artifacts) and then tries to read/verify it with a claude_code__*
// tool — but claude_code is the HOME MAC bridge, whose working dir is ~/Dev and
// which has NO /workspace directory, so every such call fails "No such file or
// directory" and the model spins (the 2026-07-02 report run burned minutes on
// claude_code__Read/__Bash "ls /workspace/artifacts/…").
//
// A claude_code file/shell call that references a /workspace path is ALWAYS a
// mistake regardless of which bridge is active (the Mac simply has no such
// path), so no bridge-router lookup is needed. This is a deterministic MECHANIC
// (Rule #1b): rather than hope the model reads a prose "use the cloud bridge"
// note, we intercept the call and hand back a tool-result steering it to the
// cloud tools (bash_run / fs_read) — the tool never runs on the Mac.
//
// Placed FIRST in the gate chain so it wins before the Trust gate. Returns
// Allow=true for everything else, so it composes cleanly.
type CloudPathRedirectGate struct{}

// claudeCodeFileTools are the claude_code verbs that take a filesystem path or a
// shell command — the ones that can reference /workspace and fail on the Mac.
var claudeCodeFileTools = map[string]bool{
	"claude_code__Read":  true,
	"claude_code__Write": true,
	"claude_code__Edit":  true,
	"claude_code__Bash":  true,
	"claude_code__LS":    true,
	"claude_code__Glob":  true,
	"claude_code__Grep":  true,
}

const cloudPathRedirectReason = "WRONG BRIDGE — this call was NOT run. %s executes on the HOME MAC (working directory ~/Dev), which has NO /workspace directory; that path exists only on your CLOUD workspace, so this would fail with \"No such file or directory\". Use the cloud tools instead: bash_run for a shell command, fs_read to read a file — they run on the cloud where /workspace lives. And note: files created by document_create / media_job already EXIST and are indexed, so you usually don't need to verify them at all."

func (CloudPathRedirectGate) Authorize(_ context.Context, _, _, toolName string, input map[string]any) GateDecision {
	if !claudeCodeFileTools[toolName] {
		return GateDecision{Allow: true}
	}
	for _, k := range []string{"file_path", "path", "command", "pattern"} {
		if v, ok := input[k].(string); ok && referencesCloudWorkspace(v) {
			return GateDecision{
				Allow:  false,
				Reason: strings.Replace(cloudPathRedirectReason, "%s", toolName, 1),
			}
		}
	}
	return GateDecision{Allow: true}
}

func (CloudPathRedirectGate) WaitForDecision(_ context.Context, _ string, _ time.Duration) (bool, string) {
	return true, ""
}

// referencesCloudWorkspace reports whether s mentions the cloud-only /workspace
// path at a PATH BOUNDARY (so it never matches "/workspaces" or an unrelated
// substring). Matches "/workspace", "/workspace/…", or "/workspace" followed by
// whitespace/quote.
func referencesCloudWorkspace(s string) bool {
	const p = "/workspace"
	for idx := 0; ; {
		i := strings.Index(s[idx:], p)
		if i < 0 {
			return false
		}
		end := idx + i + len(p)
		if end >= len(s) {
			return true
		}
		switch s[end] {
		case '/', ' ', '\t', '\n', '"', '\'', '`', ';', ')', ':':
			return true
		}
		idx = end
	}
}
