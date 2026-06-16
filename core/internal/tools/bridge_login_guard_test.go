package tools

import "testing"

// guardInteractiveLogin is the deterministic backstop for "Jarvis must sign in
// inside his OWN cloud browser, never the boss's machine". It must catch raw
// "<tool> auth login" / "<tool> login" bash invocations (which pop a browser on
// whatever box runs them) and must NOT misfire on ordinary commands — a false
// positive would block legitimate work.
func TestGuardInteractiveLogin(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		blocked bool
	}{
		// The exact failure: signing in via raw bash. All must be blocked.
		{"higgsfield auth login", "higgsfield auth login", true},
		{"sourced then auth login", "source /workspace/.jarvis/env.sh && higgsfield auth login", true},
		{"bare auth login", "auth login", true},
		{"gh auth login", "gh auth login", true},
		{"sudo gh auth login", "sudo gh auth login", true},
		{"vercel login", "vercel login", true},
		{"npm login", "npm login", true},
		{"piped login", "echo x | railway login", true},

		// Ordinary commands that merely resemble it — must NOT be blocked.
		{"git log", "git log --oneline -5", false},
		{"login in a filename", "cat login.txt", false},
		{"login as a substring", "grep login /var/log/app.log", false},
		{"higgsfield generate", "higgsfield generate create nano_banana_2 --prompt \"x\" --wait", false},
		{"higgsfield status", "higgsfield account status", false},
		{"plain echo", "echo done", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := guardInteractiveLogin(c.cmd)
			if got != c.blocked {
				t.Fatalf("guardInteractiveLogin(%q) blocked=%v, want %v", c.cmd, got, c.blocked)
			}
		})
	}
}
