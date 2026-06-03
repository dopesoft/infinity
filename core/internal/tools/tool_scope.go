package tools

import (
	"strings"
	"sync"
)

// tool_scope locks a single agent turn to an explicit allowlist of tools. It is
// the MECHANIC behind "this cron's turn may ONLY do the mail job" (Rule #1b): a
// recipe sentence saying "don't call delegate / skill_optimize / recall" is
// droppable — gpt-5.x has ignored exactly that instruction in the triage cron,
// burning whole turns in delegate/skill_optimize/recall/system_map and quitting
// with the emails still un-surfaced — so instead we make every off-task tool
// physically invisible for the session.
//
// The chain: a cron job carries a tool_scope.allow list in its TargetConfig; the
// executor registers it against the isolated session id before the turn; the
// loop's ToolVisibilityFunc (serve.go) calls HiddenBySessionScope to drop every
// registered tool name NOT permitted by the allowlist from both the schema
// bundle sent to the LLM and the dormant catalog. Hiding overrides pinning, so
// even the core-pinned footguns (delegate, system_map, recall, mem_list,
// compact_context) vanish for the locked turn. Symmetric with
// RegisterJobForSession.
//
// allow entries match a tool name either exactly or by a trailing "*" prefix
// wildcard (e.g. "composio__GMAIL_*") so a scope survives Composio tool-name
// drift without a redeploy.
var (
	toolScopeMu   sync.RWMutex
	sessionScopes = map[string][]string{}
)

// RegisterToolScopeForSession locks a session to the given allowlist. No-op on
// empty input — a session with no scope registered keeps the full toolset.
func RegisterToolScopeForSession(sessionID string, allow []string) {
	if sessionID == "" || len(allow) == 0 {
		return
	}
	cp := make([]string, 0, len(allow))
	for _, a := range allow {
		if a = strings.TrimSpace(a); a != "" {
			cp = append(cp, a)
		}
	}
	if len(cp) == 0 {
		return
	}
	toolScopeMu.Lock()
	sessionScopes[sessionID] = cp
	toolScopeMu.Unlock()
}

// UnregisterToolScopeForSession drops the lock. The executor defers this after
// the turn so the map can't grow unbounded across fires.
func UnregisterToolScopeForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	toolScopeMu.Lock()
	delete(sessionScopes, sessionID)
	toolScopeMu.Unlock()
}

// HiddenBySessionScope returns the subset of allNames the session's allowlist
// does NOT permit — the set of tool names to hide this turn. ok is false when
// the session has no scope registered (the common case: full toolset), so the
// visibility func can stay silent and leave every tool visible.
func HiddenBySessionScope(sessionID string, allNames []string) (map[string]struct{}, bool) {
	if sessionID == "" {
		return nil, false
	}
	toolScopeMu.RLock()
	allow, ok := sessionScopes[sessionID]
	toolScopeMu.RUnlock()
	if !ok {
		return nil, false
	}
	hidden := make(map[string]struct{}, len(allNames))
	for _, n := range allNames {
		if !scopeAllows(allow, n) {
			hidden[n] = struct{}{}
		}
	}
	return hidden, true
}

// scopeAllows reports whether name is permitted by the allowlist, matching
// exact names and trailing "*" prefix wildcards.
func scopeAllows(allow []string, name string) bool {
	for _, a := range allow {
		if a == name {
			return true
		}
		if strings.HasSuffix(a, "*") && strings.HasPrefix(name, strings.TrimSuffix(a, "*")) {
			return true
		}
	}
	return false
}
