package tools

import (
	"sort"
	"strings"
	"sync"
)

// ActiveSet is the per-session whitelist of tools whose full JSON-Schema
// definitions are shipped to the LLM each turn. Tools outside the active
// set still exist in the registry — they can be invoked if the model
// happens to know their exact name (e.g. tool_search returned it), and
// they show up as one-line catalog entries in the system prompt so the
// model knows they exist. The active set is what keeps 250+ Composio
// tools from drowning the context window.
//
// Mutation happens through load_tools / unload_tools native tools (called
// by the model) or programmatically (e.g. delegate spawning a child with
// its own subset). Reads on the hot path (every LLM call) are O(n).
//
// TTL semantics: each load can opt-in to a turns-until-expiry counter.
// The loop calls DecayTTL() at the start of each turn to age entries
// out automatically — keeps an exploratory `load_tools` from staying
// resident forever after the relevant work is done.
type ActiveSet struct {
	mu      sync.RWMutex
	names   map[string]int // tool name → remaining turns (0 = permanent)
	pinned  map[string]struct{}
}

// NewActiveSet returns a set seeded with the given names as permanent
// entries. Pinned names cannot be unloaded — used for the core curated
// tools (delegate, tool_search, load_tools, compact, memory ops) that
// must always be reachable for the discipline pattern to function.
func NewActiveSet(pinned []string) *ActiveSet {
	s := &ActiveSet{
		names:  make(map[string]int, len(pinned)),
		pinned: make(map[string]struct{}, len(pinned)),
	}
	for _, n := range pinned {
		s.names[n] = 0
		s.pinned[n] = struct{}{}
	}
	return s
}

// Has reports whether the named tool is currently active.
func (s *ActiveSet) Has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.names[name]
	return ok
}

// Names returns the active tool names, sorted for stable system-prompt
// rendering. Callers must not mutate the returned slice.
func (s *ActiveSet) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Load adds names to the set with an optional turn TTL. ttl=0 means
// permanent (until explicitly unloaded). ttl>0 means N agent-loop
// turns after which the entry auto-clears via DecayTTL.
func (s *ActiveSet) Load(names []string, ttl int) {
	if len(names) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		// Preserve a permanent (ttl=0) entry — never demote to a TTL.
		if existing, ok := s.names[n]; ok && existing == 0 {
			continue
		}
		s.names[n] = ttl
	}
}

// Unload removes names. Pinned names are silently ignored — the model
// can't accidentally lose access to the core discipline tools.
func (s *ActiveSet) Unload(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range names {
		if _, ok := s.pinned[n]; ok {
			continue
		}
		delete(s.names, n)
	}
}

// Replace swaps the entire non-pinned set. Used by delegate to scope a
// child session to a specific subset without inheriting the parent's
// loaded long tail.
func (s *ActiveSet) Replace(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]int, len(names)+len(s.pinned))
	for n := range s.pinned {
		next[n] = 0
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		next[n] = 0
	}
	s.names = next
}

// DecayTTL decrements every TTL'd entry by one and removes anything that
// hits zero. Called by the loop at the start of each turn.
func (s *ActiveSet) DecayTTL() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, remaining := range s.names {
		if remaining <= 0 {
			continue // permanent
		}
		remaining--
		if remaining == 0 {
			if _, pin := s.pinned[n]; !pin {
				delete(s.names, n)
				continue
			}
		}
		s.names[n] = remaining
	}
}

// Snapshot returns a copy of the name→ttl map for debug/UI.
func (s *ActiveSet) Snapshot() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.names))
	for k, v := range s.names {
		out[k] = v
	}
	return out
}
