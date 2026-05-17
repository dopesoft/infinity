package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// ToolSearch surfaces the dormant registry to the LLM. The model can call
// it with a natural-language query and receive (name, description) pairs
// for the top N matches. It does NOT load the tool - that's a separate
// step via load_tools so the model commits intentionally rather than
// vacuuming every search result into context.
//
// Matching is dumb-but-effective: case-insensitive substring + token
// overlap against name and description. Good enough for "send email" →
// composio__GMAIL_SEND_EMAIL. When the dormant catalog grows past 1k
// entries we'll want a real embedding-based search; for now this avoids
// the latency hit.
type ToolSearch struct {
	Registry *Registry
}

func (t *ToolSearch) Name() string { return "tool_search" }
func (t *ToolSearch) Description() string {
	return "Search for tools available but not currently loaded into the agent's tool surface. " +
		"Returns matching tool names and descriptions; use load_tools to make a result callable."
}
func (t *ToolSearch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What you're looking for, in natural language. E.g. 'send a slack message', 'create a github issue'.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max results to return (default 8, max 25).",
				"default":     8,
			},
		},
		"required": []string{"query"},
	}
}

type toolSearchHit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Score       int    `json:"-"`
}

func (t *ToolSearch) Execute(ctx context.Context, input map[string]any) (string, error) {
	q, _ := input["query"].(string)
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return `{"error":"query is required"}`, nil
	}
	limit := 8
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 25 {
		limit = 25
	}

	active := ActiveSetFromContext(ctx)
	activeNames := map[string]struct{}{}
	if active != nil {
		for _, n := range active.Names() {
			activeNames[n] = struct{}{}
		}
	}

	tokens := strings.Fields(q)
	hits := make([]toolSearchHit, 0, 32)
	t.Registry.mu.RLock()
	for name, tool := range t.Registry.tools {
		if _, on := activeNames[name]; on {
			continue // already loaded - no point surfacing
		}
		desc := tool.Description()
		score := scoreToolMatch(name, desc, q, tokens)
		if score <= 0 {
			continue
		}
		hits = append(hits, toolSearchHit{Name: name, Description: desc, Score: score})
	}
	t.Registry.mu.RUnlock()

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}

	body := map[string]any{
		"query":   q,
		"matches": hits,
	}
	if len(hits) == 0 {
		body["hint"] = "No matches in the dormant catalog. The tool may not be connected yet - check Settings → Connectors."
	} else {
		body["next_step"] = "Call load_tools with the names you want; their schemas will be available on the next turn."
	}
	b, _ := json.Marshal(body)
	return string(b), nil
}

// scoreToolMatch rewards exact name hits, name substring hits, and
// description token overlap. Higher score = better match. Returning 0
// drops the candidate.
func scoreToolMatch(name, desc, fullQuery string, tokens []string) int {
	nameLow := strings.ToLower(name)
	descLow := strings.ToLower(desc)
	score := 0
	if nameLow == fullQuery {
		score += 100
	}
	if strings.Contains(nameLow, fullQuery) {
		score += 25
	}
	if strings.Contains(descLow, fullQuery) {
		score += 10
	}
	for _, tok := range tokens {
		if len(tok) < 2 {
			continue
		}
		if strings.Contains(nameLow, tok) {
			score += 8
		}
		if strings.Contains(descLow, tok) {
			score += 3
		}
	}
	return score
}
