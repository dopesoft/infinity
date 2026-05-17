package bridge

import (
	"context"
	"fmt"
	"strings"
)

// PrefFetcher is the same shape tools.PreferenceFetcher uses, repeated
// here so this file doesn't depend on the tools package (avoiding an
// import cycle when agent.CompositeMemory wires it in).
type PrefFetcher func(ctx context.Context, sessionID string) Preference

// MemoryProvider satisfies the agent.MemoryProvider interface - emits
// a system-prompt prefix every turn. Used to keep Jarvis honest about
// which bridge he's actually working through right now.
//
// The overlay is intentionally short so it doesn't bloat the system
// prompt. Three lines:
//
//   Bridge status: active=… · pref=… · mac=up · cloud=up
//   Tools available now: …
//   Filesystem: …
//
// Goals:
//   - When asked "are you on my Mac or the cloud?", Jarvis can answer
//     truthfully without guessing.
//   - When asked to "edit foo.go," Jarvis picks the right tool the
//     first time (claude_code__Edit when Mac is the strong-billed
//     muscle; fs_edit when Cloud is all we have).
//   - When asked to push, Jarvis knows where the working tree lives.
type MemoryProvider struct {
	Router *Router
	Prefs  PrefFetcher
}

func (m *MemoryProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if m == nil || m.Router == nil {
		return "", nil
	}
	pref := PrefAuto
	if m.Prefs != nil {
		pref = m.Prefs(ctx, sessionID)
	}
	active, why, err := m.Router.For(ctx, pref)
	st := m.Router.Snapshot()

	var b strings.Builder
	b.WriteString("## Bridge\n")
	b.WriteString(fmt.Sprintf("Status: %s\n", m.Router.Describe(active, pref)))

	switch {
	case err != nil:
		b.WriteString(fmt.Sprintf("Active: NONE - %s. Any fs/bash/git tool call will fail. Ask the boss to bring a bridge online or flip the session preference (POST /api/bridge/session/<id>).\n", err.Error()))
	case active == nil:
		b.WriteString("Active: unknown\n")
	case active.Name() == KindMac:
		b.WriteString(fmt.Sprintf("Active: Mac (%s)\n", why))
		b.WriteString("Coding muscle: prefer `claude_code__Edit`, `claude_code__Bash`, `claude_code__Write` etc. for heavy edits - those bill against the boss's Anthropic Max subscription via Claude Code on his Mac. Use `fs_edit`, `fs_save`, `bash_run`, `git_*` when a sub-agent loop would be wasted (deterministic / single-shot).\n")
		b.WriteString("Filesystem: the boss's local Mac checkout. Commits authored as the boss's git identity.\n")
	case active.Name() == KindCloud:
		b.WriteString(fmt.Sprintf("Active: Cloud (%s)\n", why))
		b.WriteString("Tools: use the generic bridge primitives - `fs_read`, `fs_ls`, `fs_save`, `fs_edit`, `bash_run`, `git_status`, `git_diff`, `git_stage`, `git_commit`, `git_push`, `git_pull`. The `claude_code__*` schemas are intentionally not present this turn: those tools edit the boss's Mac filesystem, not this Cloud workspace volume, and calling them here would silently change the wrong tree. YOU are the only brain here - your own cognition (ChatGPT subscription via openai_oauth) covers all the decisions; no metered API spend.\n")
		b.WriteString("Filesystem: the Railway workspace volume. Commits authored as 'Jarvis Cloud <jarvis@dopesoft.io>' - diff-able from the boss's Mac commits.\n")
		b.WriteString("Sync: when the boss wants to continue on his Mac, push to GitHub and have him `git pull` there. Same in reverse.\n")
	}

	if !st.MacHealthy && !st.CloudHealthy {
		b.WriteString("Warning: both bridges report unhealthy. Tool calls will fail.\n")
	} else if pref == PrefAuto && !st.MacHealthy && st.CloudHealthy && active != nil && active.Name() == KindCloud {
		b.WriteString("Note: the boss's Mac is offline so we fell back to Cloud. If he comes back online, the next turn will auto-route to Mac.\n")
	}
	return b.String(), nil
}
