package main

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Keeping the box's Claude Code current.
//
// The CLI is installed into the IMAGE (`npm install -g` at build time) and
// npm's global prefix here is /usr/lib/node_modules, not the /workspace
// volume. So nothing the CLI does to itself survives a restart, and the
// version only ever changed when that Docker layer happened to rebuild - which
// caching makes rare and invisible. On 2026-09-01 that meant the boss's Mac
// could run a model his cloud box could not, and neither number was written
// down anywhere he could see.
//
// So the box updates itself: once at boot, then daily. It is deliberately NOT
// pinned - the boss's call, and his words were "it's from Anthropic, I trust
// new CLI versions". The image's baked copy is the floor: if the network is
// down or npm fails, the box keeps serving on what it already has and says so.
//
// A version bump never disturbs work in flight. Node loads a script at spawn,
// so a `claude` already running keeps the code it started with and only the
// NEXT launch picks up the new one.
const (
	claudePkg      = "@anthropic-ai/claude-code@latest"
	updateEvery    = 24 * time.Hour
	updateTimeout  = 5 * time.Minute
	versionTimeout = 20 * time.Second
)

// startClaudeUpdater runs the update in the background, forever. Called once
// from main; never blocks the listener, because a box that cannot answer while
// npm thinks is worse than a box one version behind.
func startClaudeUpdater() {
	go func() {
		for {
			updateClaudeOnce()
			time.Sleep(updateEvery)
		}
	}()
}

func updateClaudeOnce() {
	before := claudeVersion()
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "install", "-g", claudePkg).CombinedOutput()
	if err != nil {
		// Loud, because a box silently stuck on an old CLI is exactly how a
		// model the boss selected starts failing on one bridge and not the
		// other. Not fatal: the image's version still works.
		log.Printf("claude update failed, staying on %s: %v: %s",
			or(before, "unknown"), err, strings.TrimSpace(tail(string(out), 300)))
		return
	}
	after := claudeVersion()
	switch {
	case after == "":
		log.Printf("claude update ran but the CLI does not report a version")
	case before == after:
		infoLog.Printf("claude code: already current (%s)", after)
	default:
		infoLog.Printf("claude code: updated %s -> %s", or(before, "unknown"), after)
	}
}

// claudeVersion returns the installed version ("2.1.258"), or "" when the CLI
// cannot be asked.
func claudeVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return ""
	}
	// "2.1.258 (Claude Code)" -> "2.1.258"
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), " ", 2)[0])
}

func or(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
