package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Raw `claude -p` via a shell tool is banned; Claude Code runs through
// code_agent.
//
// 2026-08-26: mid-build the chat model ran `claude -p "Work in
// /Users/n0m4d/Dev/infinity ..."` through claude_code__Bash. That run had no
// mem_runs row (invisible in Agent Work), no pid file (the Stop button and a
// mid-turn steer could not kill it), no delete gate, and its "You're out of
// extra usage" reply was never parsed, so the boss's spent Claude plan stayed
// invisible. code_agent owns all of that. Rule #1b: the mechanic is a gate
// in code, applied at BOTH shell entry points (bash_run here and the
// claude_code__Bash gate in proactive), not a sentence in the soul.

// rawClaudeCmdRe matches an invocation of the claude CLI in print / resume
// mode: at the start of the command, after a shell separator, or behind
// sudo/nohup/exec/env. Flags before the mode flag are allowed
// (`claude --model x -p`). It does NOT match `claude --version`, `which
// claude`, `claude doctor`, or the word inside a path or string.
var rawClaudeCmdRe = regexp.MustCompile(`(?i)(^|[;&|(]\s*|\b(?:sudo|nohup|exec|env)\s+)claude(?:\s+--?[\w-]+(?:=\S+|\s+[^-\s]\S*)?)*\s+(-p|--print|-c|--continue|-r|--resume)(\s|$|=)`)

// IsRawClaudeCodeCmd reports whether a bash command launches Claude Code
// directly (print/continue/resume mode).
func IsRawClaudeCodeCmd(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd != "" && rawClaudeCmdRe.MatchString(cmd)
}

// RawClaudeCodeRedirect is the tool result handed back instead of running it.
func RawClaudeCodeRedirect() string {
	payload := map[string]any{
		"blocked": "raw_claude_code_via_bash",
		"why": "Running `claude -p` through a shell tool gives the boss no run to watch or stop, skips the delete gate, " +
			"and hides Claude's own errors (an 'out of usage' reply came back unread this way).",
		"do_this_instead": "Call code_agent with the same task (and repo). It launches Claude Code on the Mac, books a run the " +
			"boss can see and stop, gates deletes, and reports Claude's real result or failure. If code_agent says Claude " +
			"Code is out of usage, do not work around it with bash; tell the boss.",
	}
	out, _ := json.Marshal(payload)
	return string(out)
}
