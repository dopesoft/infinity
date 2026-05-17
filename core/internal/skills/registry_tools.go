package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires five agent-callable tools backed by the Registry +
// Runner. The agent can always reach for these regardless of which skills
// happen to be installed.
//
//   skills_list      - enumerate installed skills
//   skills_invoke    - execute a skill by name
//   skills_discover  - fuzzy search over trigger phrases / descriptions
//   skills_history   - recent runs of a single skill
//   skill_create     - author a NEW skill and make it live (self-authoring)
func RegisterTools(reg *tools.Registry, registry *Registry, runner *Runner) {
	reg.Register(&listTool{r: registry})
	reg.Register(&invokeTool{r: registry, runner: runner})
	reg.Register(&discoverTool{r: registry})
	reg.Register(&historyTool{r: registry})
	reg.Register(&skillCreateTool{r: registry})
}

// ---- skills.list -----------------------------------------------------------

type listTool struct{ r *Registry }

func (t *listTool) Name() string        { return "skills_list" }
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

func (t *invokeTool) Name() string        { return "skills_invoke" }
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
		// Skill is LLM-only - return its formatted prompt for the parent
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

func (t *discoverTool) Name() string { return "skills_discover" }
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

func (t *historyTool) Name() string        { return "skills_history" }
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

// ---- skill_create ----------------------------------------------------------
//
// The closed-loop self-authoring path. Rule #1 substrate: the agent
// assembles a workflow described in natural language into a durable,
// reusable recipe. The judgment - the rubric, the steps, the "hard rules"
// - lives in the SKILL.md body the agent writes, never in Go. A low-risk
// recipe goes live immediately (invocable this session, durable across
// restarts via the boot-time materialize + reload chain). Anything riskier
// is filed as a candidate for the boss to approve.

type skillCreateTool struct{ r *Registry }

func (t *skillCreateTool) Name() string { return "skill_create" }
func (t *skillCreateTool) Description() string {
	return "Author a NEW skill and make it live. A skill is a reusable recipe - " +
		"its body is the instruction set you (or a future session) follow: \"hit " +
		"this API, pull the data, analyze it, surface the result.\" Create one " +
		"when the boss describes a workflow worth keeping, or when you notice a " +
		"repeatable multi-step pattern. Low-risk recipe skills (risk_level=low, no " +
		"executable file) go LIVE IMMEDIATELY - invocable this session via " +
		"skills_invoke and durable across restarts. Anything riskier is filed as a " +
		"candidate for the boss to approve in the Skills tab. The judgment lives " +
		"in the `body` (the rubric, the steps, the hard rules) - that IS the " +
		"skill. Returns the skill name and whether it went live or pending."
}
func (t *skillCreateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "kebab-case skill name, 3-40 chars, letter-led (e.g. 'inbox-triage', 'weekly-digest').",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "One or two sentences: what the skill does and when to fire it.",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "The SKILL.md body in Markdown - the actual recipe. Steps, the rubric, the judgment ('hard rules'). This IS the skill; write it like instructions to yourself.",
			},
			"trigger_phrases": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Natural-language phrases that should fire this skill.",
			},
			"risk_level": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "medium", "high", "critical"},
				"description": "low = pure recipe, goes live immediately. medium+ = filed as a candidate for boss approval. Default low.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Why this rose to skill-worthy now (shown to the boss on the candidate path).",
			},
		},
		"required": []string{"name", "description", "body"},
	}
}
func (t *skillCreateTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	name, _ := in["name"].(string)
	name = strings.TrimSpace(name)
	if !validSkillName(name) {
		return "", fmt.Errorf("skill_create: name must be kebab-case, 3-40 chars, letter-led (got %q)", name)
	}
	desc, _ := in["description"].(string)
	if strings.TrimSpace(desc) == "" {
		return "", errors.New("skill_create: description is required")
	}
	body, _ := in["body"].(string)
	if strings.TrimSpace(body) == "" {
		return "", errors.New("skill_create: body is required")
	}
	riskStr, _ := in["risk_level"].(string)
	risk := RiskLevel(strings.ToLower(strings.TrimSpace(riskStr)))
	if risk == "" {
		risk = RiskLow
	}
	if !risk.Valid() {
		return "", fmt.Errorf("skill_create: invalid risk_level %q", risk)
	}
	reasoning, _ := in["reasoning"].(string)
	var triggers []string
	if raw, ok := in["trigger_phrases"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				triggers = append(triggers, strings.TrimSpace(s))
			}
		}
	}

	// Low-risk recipe skills go live immediately. Anything riskier is
	// filed as a candidate for the boss to approve in the Skills tab.
	if risk == RiskLow {
		sk := &Skill{
			Name:           name,
			Version:        "1.0.0",
			Description:    desc,
			TriggerPhrases: triggers,
			RiskLevel:      RiskLow,
			Confidence:     0.6,
			Body:           strings.TrimSpace(body),
			Source:         SourceAgent,
			Status:         StatusActive,
		}
		if err := t.r.Put(ctx, sk); err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"status":  "live",
			"name":    name,
			"version": sk.Version,
			"message": fmt.Sprintf("Skill %q is live now - invoke it with skills_invoke. It persists across restarts.", name),
		})
		return string(out), nil
	}

	// Candidate path - needs the Store.
	if t.r.store == nil {
		return "", errors.New("skill_create: medium+ risk skills need a database (no Store configured)")
	}
	id, err := t.r.store.InsertProposal(ctx, name, desc, reasoning, strings.TrimSpace(body), string(risk))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"status":     "pending",
		"id":         id,
		"name":       name,
		"risk_level": string(risk),
		"message":    fmt.Sprintf("Skill %q filed as a candidate (risk=%s). The boss promotes it in the Skills tab.", name, risk),
	})
	return string(out), nil
}

// validSkillName enforces kebab-case: 3-40 chars, lowercase letters / digits
// / hyphens, must start with a letter, no leading/trailing/double hyphen.
func validSkillName(s string) bool {
	if len(s) < 3 || len(s) > 40 {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	prevHyphen := false
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			prevHyphen = false
		case r == '-':
			if prevHyphen || i == len(s)-1 {
				return false
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return true
}
