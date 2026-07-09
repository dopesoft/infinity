package tools

import (
	"context"
	"strings"
	"sync"
)

// CLICatalog is the read side of the installed cloud CLI extensions (yt-dlp,
// ffmpeg, …). It exists so bash_run and the wrong-bridge gate can know, without
// asking the LLM, that a command targets a binary which lives on the CLOUD
// workspace volume and needs the persistent env sourced first.
//
// Declared here, implemented by extensions.Manager, attached at wiring time —
// package extensions imports package tools, so the dependency cannot run the
// other way. Same shape as skills.FrontierSampler.
type CLICatalog interface {
	// CloudCLIBinaries returns the binary names of every ACTIVE cli extension
	// (e.g. ["yt-dlp", "ffmpeg"]). Empty on any error: a catalog lookup must
	// never fail a tool call.
	CloudCLIBinaries(ctx context.Context) []string
	// CloudEnvPrelude returns the shell prefix that puts those binaries on PATH
	// and points HOME at the persistent volume, e.g. "source /path/env.sh && ".
	CloudEnvPrelude() string
}

var (
	cliCatalogMu sync.RWMutex
	cliCatalog   CLICatalog
)

// AttachCLICatalog wires the installed-extension catalog. Called once at boot,
// after the extensions Manager exists. Without it CloudCLICommand always
// returns false and every caller behaves exactly as it did before.
func AttachCLICatalog(c CLICatalog) {
	cliCatalogMu.Lock()
	defer cliCatalogMu.Unlock()
	cliCatalog = c
}

func activeCLICatalog() CLICatalog {
	cliCatalogMu.RLock()
	defer cliCatalogMu.RUnlock()
	return cliCatalog
}

// CloudCLICommand reports whether cmd invokes a cloud-resident CLI extension,
// and returns the prelude that must precede it for the binary to be on PATH.
//
// WHY THIS IS CODE AND NOT A SENTENCE (Rule #1b). Three separate mechanics used
// to live as prose the model had to remember on every call:
//
//  1. "Run them via bash_run"                     — which tool
//  2. "prefix with `source /workspace/.jarvis/env.sh &&`" — which PATH
//  3. "Pass bridge=\"cloud\" for cloud-resident CLI tools" — which machine
//
// Miss any one and the tool reports "command not found" or silently runs on the
// wrong box. On 2026-07-09 Jarvis told the boss he could not pull a YouTube
// transcript while yt-dlp sat installed, active, and version-checked on the
// cloud workspace. The catalog turns all three into things the call site
// enforces, so the recipe cannot forget them.
func CloudCLICommand(ctx context.Context, cmd string) (prelude string, ok bool) {
	c := activeCLICatalog()
	if c == nil || strings.TrimSpace(cmd) == "" {
		return "", false
	}
	bins := c.CloudCLIBinaries(ctx)
	if len(bins) == 0 {
		return "", false
	}
	set := make(map[string]bool, len(bins))
	for _, b := range bins {
		if b = strings.TrimSpace(b); b != "" {
			set[b] = true
		}
	}
	if !invokesAny(cmd, set) {
		return "", false
	}
	pre := c.CloudEnvPrelude()
	// Idempotent: a command that already sources the env keeps exactly one copy.
	if pre == "" || strings.Contains(cmd, strings.TrimSuffix(strings.TrimSpace(pre), "&&")) {
		return "", true
	}
	return pre, true
}

// shellSeparators split a command line into the segments that each start with a
// binary invocation: `source env.sh && yt-dlp … | head` has three.
var shellSeparators = []string{"&&", "||", ";", "|", "\n"}

// invokesAny reports whether any segment of cmd calls one of the given binaries
// as its command word. Checking the command word (not a substring) keeps
// `echo "install yt-dlp"` from being treated as a yt-dlp invocation.
func invokesAny(cmd string, bins map[string]bool) bool {
	segments := []string{cmd}
	for _, sep := range shellSeparators {
		var next []string
		for _, s := range segments {
			next = append(next, strings.Split(s, sep)...)
		}
		segments = next
	}
	// EVERY segment gets checked, not just the first: `source env.sh && yt-dlp`
	// and `echo x | ffmpeg …` both invoke a cloud binary in a later segment, and
	// those are the shapes a recipe actually writes.
	for _, seg := range segments {
		for _, f := range strings.Fields(seg) {
			// Skip what precedes a command word: env assignments (FOO=bar cmd)
			// and the usual wrappers.
			if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
				continue
			}
			if f == "sudo" || f == "command" || f == "exec" {
				continue
			}
			// Strip any path prefix so `~/.local/bin/yt-dlp` still matches.
			if i := strings.LastIndex(f, "/"); i >= 0 {
				f = f[i+1:]
			}
			if bins[f] {
				return true
			}
			// This segment's command word wasn't one of ours. Its arguments
			// can't be either — `git commit -m "fix yt-dlp"` is a git command.
			break
		}
	}
	return false
}
