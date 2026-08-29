package tools

import (
	"regexp"
	"strings"
)

// Refusing the hand-rolled job babysitter.
//
// 2026-08-29, session 0ac9a94c. Two Claude Code jobs were running at once, and
// Jarvis tried to referee them from bash:
//
//	kill 72481 72484 2>/dev/null || true
//	while [ ! -f /tmp/inf-code/0a4074b6-….status ]; do sleep 2; done
//	printf 'exit='; tr -d '\n' < /tmp/inf-code/0a4074b6-….status
//
// The boss watched that one row spin for over fifteen minutes with no output,
// because a bash command says nothing until it exits. Every part of it is
// already a solved problem in the substrate — the job reports back on its own
// when it lands, its run row carries live progress, and only the boss may stop
// it — and every part of it was reinvented, worse, in a shell.
//
// This is the mechanic that makes it impossible rather than merely discouraged
// (Rule #1b): a prompt line saying "don't poll" is a line the runtime model
// drops. `/tmp/inf-code` is Infinity's OWN job directory, so a command reaching
// into it is never legitimate work — it is always the agent trying to manage
// machinery it should be asking about.

var (
	// A loop whose exit condition is a file appearing / a process dying, with
	// a sleep in it. This is a waiter, not work.
	waitLoopRe = regexp.MustCompile(`(?s)\b(while|until)\b.*\bsleep\b`)
	// Signals aimed at a bare pid. `pkill -f my-dev-server` is ordinary work;
	// `kill 72481` is the agent swinging at a process it looked up.
	killPidRe = regexp.MustCompile(`\b(kill|pkill)\b[^|;&\n]*?\s-?\d{2,}\b`)
)

// codeAgentJobDir is Infinity's own scratch directory for detached coding
// jobs. Nothing the agent legitimately does in a shell touches it.
const codeAgentJobDir = "/tmp/inf-code"

// guardJobBabysitting reports the redirect for a command that is trying to
// wait on, poll, or kill a coding job by hand. blocked=false means the command
// is ordinary work and runs untouched.
func guardJobBabysitting(cmd string) (redirect string, blocked bool) {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return "", false
	}

	if strings.Contains(c, codeAgentJobDir) {
		return "BLOCKED - that is Infinity's own job directory, not a place to work.\n\n" +
			"`" + codeAgentJobDir + "` holds the live files of a detached Claude Code run: its output stream, its pid, its exit status. " +
			"Reading or waiting on them by hand reinvents machinery that already exists and works:\n\n" +
			"- **To know what it is doing right now**: it is on the run row, updated every 15 seconds — the boss can already see it in the dock above his composer. Just tell him what it says.\n" +
			"- **To be told when it finishes**: you already are. A detached job reports its real result back into this chat on its own. You do not have to wait for it, and you must not.\n" +
			"- **To pick it back up afterwards**: `code_agent` with `resume_session`.\n\n" +
			"Answer the boss now with what you know. Do not run anything against that directory.", true
	}

	if waitLoopRe.MatchString(c) {
		return "BLOCKED - that command waits instead of working.\n\n" +
			"A bash loop that sleeps until something happens produces NO output until it exits, so from the boss's side it is an " +
			"indistinguishable spinner — one of these ran for over fifteen minutes in front of him. It also blocks this tool, and the " +
			"bridge kills it at five minutes anyway, so it cannot even do the job it was written for.\n\n" +
			"If you are waiting on a long job, you do not need to: it reports back into this chat by itself when it lands. If you are " +
			"waiting on something else, use `watch_until` — it settles durably in the background and tells you, and it survives this turn " +
			"ending. Run the command that does the WORK, not the one that waits.", true
	}

	if killPidRe.MatchString(c) {
		return "BLOCKED - do not kill processes by pid.\n\n" +
			"A pid you looked up is not a thing you can reason about safely: on the boss's Mac it may be his editor, his dev server, or a " +
			"build he is running himself. If it is a coding job you want stopped, only the boss stops those — a job that is still working " +
			"has not failed, and stopping it throws away work that is landing on disk.\n\n" +
			"Tell him what is running and ask, or leave it alone. If the concern is two jobs at once, that cannot happen any more: a second " +
			"launch is refused while one is live.", true
	}

	return "", false
}
