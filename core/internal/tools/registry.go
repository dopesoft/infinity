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
// only. Tools whose names aren't in `active` are silently skipped — they
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
// renders this into the system prompt — ~30 tokens per entry is the
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
