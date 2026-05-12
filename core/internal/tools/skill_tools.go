package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterSkillTools wires the agent-callable skill-pipeline tools.
//
//   - skill_propose: the agent calls this mid-conversation when it recognizes
//     a reusable pattern. Inserts a row into mem_skill_proposals as a
//     candidate; the user reviews + promotes/rejects in the Skills tab.
//   - skill_optimize: the agent calls this to suggest an update to an
//     existing skill. Drops a proposal with parent_skill set so the diff is
//     visible. (Distinct from the GEPA optimizer — this is the agent's own
//     edit suggestion, not a Pareto search result.)
//
// No-op when pool is nil. Skills don't need an embedder.
func RegisterSkillTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&skillProposeTool{pool: pool})
	r.Register(&skillOptimizeTool{pool: pool})
}

// ── skill_propose ───────────────────────────────────────────────────────────

type skillProposeTool struct {
	pool *pgxpool.Pool
}

func (t *skillProposeTool) Name() string { return "skill_propose" }
func (t *skillProposeTool) Description() string {
	return "Propose a brand-new skill from the current conversation. Call when you " +
		"notice a reusable pattern (the boss keeps asking you to do X with Y inputs, " +
		"a multi-step workflow that worked well, etc). Drops a candidate into the " +
		"Skills tab for the boss to promote or reject. Returns the candidate id."
}
func (t *skillProposeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "kebab-case skill name (e.g. 'scaffold-vite-react'). 3-40 chars.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "One or two sentences explaining what the skill does and when to fire it.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Why this rose to skill-worthy now. What pattern did you notice?",
			},
			"skill_md": map[string]any{
				"type":        "string",
				"description": "Draft SKILL.md body in Markdown. Should include the steps the skill performs.",
			},
			"trigger_phrases": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Natural-language phrases that should fire this skill.",
			},
			"risk_level": map[string]any{
				"type":    "string",
				"enum":    []string{"low", "medium", "high", "critical"},
				"default": "low",
			},
		},
		"required": []string{"name", "description", "skill_md"},
	}
}

func (t *skillProposeTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is required")
	}
	desc, _ := input["description"].(string)
	if strings.TrimSpace(desc) == "" {
		return "", errors.New("description is required")
	}
	skillMD, _ := input["skill_md"].(string)
	if strings.TrimSpace(skillMD) == "" {
		return "", errors.New("skill_md is required")
	}
	reasoning, _ := input["reasoning"].(string)
	risk, _ := input["risk_level"].(string)
	if risk == "" {
		risk = "low"
	}

	id := uuid.NewString()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
			(id, name, description, reasoning, skill_md, risk_level, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'candidate', NOW())
	`, id, name, desc, reasoning, skillMD, risk)
	if err != nil {
		return "", fmt.Errorf("insert proposal: %w", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status":      "proposed",
		"id":          id,
		"name":        name,
		"description": desc,
		"reasoning":   reasoning,
		"risk_level":  risk,
		"message": fmt.Sprintf(
			"Proposed candidate skill %q. The boss will see it in the Skills tab to promote or reject.",
			name,
		),
	})
	return string(out), nil
}

// ── skill_optimize ──────────────────────────────────────────────────────────

type skillOptimizeTool struct {
	pool *pgxpool.Pool
}

func (t *skillOptimizeTool) Name() string { return "skill_optimize" }
func (t *skillOptimizeTool) Description() string {
	return "Suggest an update to an existing skill. Call when you've noticed " +
		"that the current SKILL.md is missing a step, has stale info, or could be " +
		"sharper based on this session. Drops a candidate tagged as parent_skill " +
		"so the diff is reviewable in the Skills tab."
}
func (t *skillOptimizeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"parent_skill": map[string]any{
				"type":        "string",
				"description": "Name of the existing skill being refined (e.g. 'scaffold-vite-react').",
			},
			"new_skill_md": map[string]any{
				"type":        "string",
				"description": "Full replacement SKILL.md body in Markdown.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Why the update is needed. What gap did you find?",
			},
		},
		"required": []string{"parent_skill", "new_skill_md", "reasoning"},
	}
}

func (t *skillOptimizeTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	parent, _ := input["parent_skill"].(string)
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "", errors.New("parent_skill is required")
	}
	skillMD, _ := input["new_skill_md"].(string)
	if strings.TrimSpace(skillMD) == "" {
		return "", errors.New("new_skill_md is required")
	}
	reasoning, _ := input["reasoning"].(string)
	if strings.TrimSpace(reasoning) == "" {
		return "", errors.New("reasoning is required")
	}

	id := uuid.NewString()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
			(id, name, description, reasoning, skill_md, risk_level, status,
			 parent_skill, created_at)
		VALUES ($1, $2, $3, $4, $5, 'low', 'candidate', $6, NOW())
	`, id, parent+"-update", "Updated SKILL.md for "+parent, reasoning, skillMD, parent)
	if err != nil {
		return "", fmt.Errorf("insert update proposal: %w", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status":       "proposed",
		"id":           id,
		"parent_skill": parent,
		"message": fmt.Sprintf(
			"Proposed update for skill %q. The boss reviews + applies in the Skills tab.",
			parent,
		),
	})
	return string(out), nil
}
