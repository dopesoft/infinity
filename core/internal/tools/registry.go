// Package tools provides the agent's tool registry and the Tool interface
// used by both native tools and MCP-bridged tools.
package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dopesoft/infinity/core/internal/llm"
)

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(ctx context.Context, input map[string]any) (string, error)
}

// ReadOnlyTool is an OPTIONAL extension interface. Tools that implement
// it declare themselves explicitly as reads (true) or mutations (false),
// killing the system_map heuristic that classified by name suffix. The
// default - when a tool does NOT implement it - is still the suffix
// heuristic, so existing tools continue to work without changes.
//
// Implement on any new tool whose name doesn't match the `_list /
// _search / _get / _status / _history` convention but is nevertheless
// a read.
type ReadOnlyTool interface {
	Tool
	ReadOnly() bool
}

// IsReadOnly returns the read/mutate classification for a tool, using
// the explicit interface if implemented and falling back to the suffix
// heuristic otherwise. Used by system_map to bucket tools into
// list_tools vs mutate_tools.
func IsReadOnly(t Tool) bool {
	if rt, ok := t.(ReadOnlyTool); ok {
		return rt.ReadOnly()
	}
	return isListLikeName(t.Name())
}

// isListLikeName is the suffix-based fallback. Kept here (not in
// system_map.go) so any caller can use the same classification.
func isListLikeName(name string) bool {
	suffixes := []string{"_list", "_search", "_get", "_status", "_history", "_recall", "_discover"}
	for _, s := range suffixes {
		if hasSuffix(name, s) {
			return true
		}
	}
	switch name {
	case "recall", "skills_list", "skills_history", "skills_discover",
		"workflow_list", "workflow_status", "cron_list", "extension_list",
		"goal_list", "entity_search", "entity_get", "budget_status":
		return true
	}
	return false
}

// hasSuffix is a tiny local helper to keep this file dep-free.
func hasSuffix(s, sfx string) bool {
	return len(s) >= len(sfx) && s[len(s)-len(sfx):] == sfx
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns a snapshot of every registered tool as a (name → Tool)
// map. Snapshot semantics: the returned map is a copy, so callers can
// iterate without holding the registry lock. Used by system_map and
// any other introspection path that needs both names and types in one
// pass - avoids the Names() + Get(name) round-trip that the v1 impl
// used.
func (r *Registry) All() map[string]Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Tool, len(r.tools))
	for n, t := range r.tools {
		out[n] = t
	}
	return out
}

func (r *Registry) Definitions() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Execute(ctx context.Context, call llm.ToolCall) (string, error) {
	t, ok := r.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", call.Name)
	}
	return t.Execute(ctx, call.Input)
}

// DefinitionsFor returns the JSON-schema tool defs for the named subset
// only. Tools whose names aren't in `active` are silently skipped - they
// remain executable (Get/Execute still work) but their schemas don't go
// to the LLM. This is the hot path of Phase 1 lazy loading: instead of
// every connected MCP dumping ~60 tools into the system prompt, only the
// session's active set materialises as definitions.
func (r *Registry) DefinitionsFor(active []string) []llm.ToolDef {
	if len(active) == 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolDef, 0, len(active))
	for _, name := range active {
		t, ok := r.tools[name]
		if !ok {
			continue
		}
		out = append(out, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CatalogEntry is a name + one-line description for tools that are
// registered but not currently active. Rendered into the system prompt
// as a compact "tools you can request" list so the model knows what
// exists and can ask tool_search to materialise schemas it needs.
type CatalogEntry struct {
	Name        string
	Description string
}

// DormantCatalog returns the (name, description) for every registered
// tool whose name is NOT in `active`. Sorted, deterministic. The loop
// renders this into the system prompt - ~30 tokens per entry is the
// price of "the model knows the long tail exists" without paying the
// full schema cost.
func (r *Registry) DormantCatalog(active []string) []CatalogEntry {
	activeSet := make(map[string]struct{}, len(active))
	for _, n := range active {
		activeSet[n] = struct{}{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CatalogEntry, 0)
	for name, t := range r.tools {
		if _, on := activeSet[name]; on {
			continue
		}
		out = append(out, CatalogEntry{Name: name, Description: t.Description()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
