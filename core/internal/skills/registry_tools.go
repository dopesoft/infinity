package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires four agent-callable tools backed by the Registry +
// Runner. The agent can always reach for these regardless of which skills
// happen to be installed.
//
//   skills.list      — enumerate installed skills
//   skills.invoke    — execute a skill by name
//   skills.discover  — fuzzy search over trigger phrases / descriptions
//   skills.history   — recent runs of a single skill
func RegisterTools(reg *tools.Registry, registry *Registry, runner *Runner) {
	reg.Register(&listTool{r: registry})
	reg.Register(&invokeTool{r: registry, runner: runner})
	reg.Register(&discoverTool{r: registry})
	reg.Register(&historyTool{r: registry})
}

// ---- skills.list -----------------------------------------------------------

type listTool struct{ r *Registry }

func (t *listTool) Name() string        { return "skills.list" }
func (t *listTool) Description() string { return "List every installed skill with risk level + confidence." }
func (t *listTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *listTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	all := t.r.All()
	out := make([]map[string]any, 0, len(all))
	for _, s := range all {
		out = append(out, map[string]any{
			"name":        s.Name,
			"version":     s.Version,
			"description": s.Description,
			"risk_level":  s.RiskLevel,
			"confidence":  s.Confidence,
			"network_egress": s.NetworkEgress,
		})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// ---- skills.invoke ---------------------------------------------------------

type invokeTool struct {
	r      *Registry
	runner *Runner
}

func (t *invokeTool) Name() string        { return "skills.invoke" }
func (t *invokeTool) Description() string { return "Execute a skill by name with the given args." }
func (t *invokeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Skill name."},
			"args": map[string]any{"type": "object", "description": "Skill input arguments."},
		},
		"required": []any{"name"},
	}
}
func (t *invokeTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name, _ := in["name"].(string)
	if name == "" {
		return "", fmt.Errorf("skills.invoke: name is required")
	}
	args := map[string]any{}
	if a, ok := in["args"].(map[string]any); ok {
		args = a
	}
	skill, ok := t.r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown skill: %s", name)
	}
	if skill.ImplPath == "" {
		// Skill is LLM-only — return its formatted prompt for the parent
		// agent to fold into its own context. The LLM caller is in charge
		// of executing the instruction. This is the pattern used by the
		// majority of OpenClaw and Hermes skills.
		return FormatLLMPrompt(skill, args), nil
	}
	res, _, err := t.runner.Invoke(ctx, "", name, args, "conversation")
	if err != nil {
		if res.Stdout != "" || res.Stderr != "" {
			return fmt.Sprintf("ERROR: %v\n--- stdout ---\n%s\n--- stderr ---\n%s", err, res.Stdout, res.Stderr), nil
		}
		return "", err
	}
	return res.Stdout, nil
}

// ---- skills.discover -------------------------------------------------------

type discoverTool struct{ r *Registry }

func (t *discoverTool) Name() string { return "skills.discover" }
func (t *discoverTool) Description() string {
	return "Semantic / phrase search over installed skills. Returns ranked matches."
}
func (t *discoverTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query."},
			"limit": map[string]any{"type": "integer", "description": "Max results (default 5)."},
		},
		"required": []any{"query"},
	}
}
func (t *discoverTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	q, _ := in["query"].(string)
	if strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("skills.discover: query is required")
	}
	limit := 5
	if v, ok := in["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	matches := t.r.Match(q, limit)
	out := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		out = append(out, map[string]any{
			"name":        m.Skill.Name,
			"version":     m.Skill.Version,
			"description": m.Skill.Description,
			"risk_level":  m.Skill.RiskLevel,
			"score":       m.Score,
			"matched_phrase": m.Phrase,
		})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// ---- skills.history --------------------------------------------------------

type historyTool struct{ r *Registry }

func (t *historyTool) Name() string        { return "skills.history" }
func (t *historyTool) Description() string { return "Recent runs of a skill (success/duration/output)." }
func (t *historyTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
}
func (t *historyTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name, _ := in["name"].(string)
	if name == "" {
		return "", fmt.Errorf("skills.history: name is required")
	}
	limit := 10
	if v, ok := in["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	store := t.r.store
	if store == nil {
		return "history is unavailable (no database configured)", nil
	}
	runs, err := store.RecentRuns(ctx, name, limit)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(runs, "", "  ")
	return string(b), nil
}
