package tools

import "testing"

// IsRawClaudeCodeCmd must catch the exact invocation the chat model used to
// bypass code_agent, and must not misfire on ordinary commands that mention
// claude (a version check, a path, a grep).
func TestIsRawClaudeCodeCmd(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		blocked bool
	}{
		{"the 2026-08-26 call", `claude -p "Work in /Users/n0m4d/Dev/infinity. Complete one narrow backend task only." --output-format json`, true},
		{"print long flag", `claude --print "fix the build"`, true},
		{"model before mode", `claude --model opus -p "do it"`, true},
		{"continue", `cd ~/Dev/infinity && claude --continue`, true},
		{"resume short", `claude -r abc123`, true},
		{"nohup detached", `nohup claude -p "$TASK" > out.json 2>&1 &`, true},
		{"after separator", `export INF_TASK=x; claude -p "$INF_TASK"`, true},
		{"sudo", `sudo claude -p hi`, true},

		{"version", `claude --version`, false},
		{"which", `which claude`, false},
		{"doctor", `claude doctor`, false},
		{"path mention", `ls ~/.claude/projects`, false},
		{"grep mention", `grep -rn "claude -p" core/internal/tools`, false},
		{"go test", `go test ./...`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRawClaudeCodeCmd(c.cmd); got != c.blocked {
				t.Fatalf("IsRawClaudeCodeCmd(%q) = %v, want %v", c.cmd, got, c.blocked)
			}
		})
	}
}
