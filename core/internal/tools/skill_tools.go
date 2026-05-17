package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/proposals"
)

// Drafter is the minimal LLM interface skill_optimize needs to run a
// Haiku merge when more than one open candidate exists for the same
// parent_skill. nil-safe - without a drafter, merges fall back to
// "latest body wins, append reasoning to changes_log" (still no dupe
// rows, just no LLM-flagged conflicts).
type SkillToolsDrafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

// RegisterSkillTools wires the agent-callable skill-pipeline tools.
//
//   - skill_propose: the agent calls this mid-conversation when it recognizes
//     a reusable pattern. Inserts a row into mem_skill_proposals as a
//     candidate; the user reviews + promotes/rejects in the Skills tab.
//   - skill_optimize: the agent calls this to suggest an update to an
//     existing skill. Drops a proposal with parent_skill set so the diff is
//     visible. (Distinct from the GEPA optimizer - this is the agent's own
//     edit suggestion, not a Pareto search result.)
//
// No-op when pool is nil. Skills don't need an embedder.
func RegisterSkillTools(r *Registry, pool *pgxpool.Pool, drafter SkillToolsDrafter) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&skillProposeTool{pool: pool})
	r.Register(&skillOptimizeTool{pool: pool, drafter: drafter})
	r.Register(&skillProposalGetTool{pool: pool})
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
				"type":        "string",
				"enum":        []string{"low", "medium", "high", "critical"},
				"default":     "low",
				"description": "Execution risk/sandbox level, NOT importance. low = prompt-only recipe, safe to run. medium+ = writes files, calls paid/external APIs, sends messages, or changes external state.",
			},
			"importance": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     100,
				"description": "Strategic value to Infinity's AGI behavior. Use 90-100 for core memory/self-improvement/proactivity/tool-reliability skills even when risk_level is low.",
			},
			"importance_reason": map[string]any{
				"type":        "string",
				"description": "One sentence explaining why this skill is strategically important or routine.",
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
	importance := intFrom(input, "importance")
	if importance <= 0 {
		importance = inferSkillImportance(name, desc, reasoning, skillMD)
	}
	importanceReason, _ := input["importance_reason"].(string)
	importanceReason = strings.TrimSpace(importanceReason)
	if importanceReason == "" {
		importanceReason = inferSkillImportanceReason(name, desc, reasoning, skillMD)
	}

	id := uuid.NewString()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
			(id, name, description, reasoning, skill_md, risk_level, importance, importance_reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'candidate', NOW())
	`, id, name, desc, reasoning, skillMD, risk, importance, importanceReason)
	if err != nil {
		return "", fmt.Errorf("insert proposal: %w", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status":            "proposed",
		"id":                id,
		"name":              name,
		"description":       desc,
		"reasoning":         reasoning,
		"risk_level":        risk,
		"importance":        importance,
		"importance_reason": importanceReason,
		"message": fmt.Sprintf(
			"Proposed candidate skill %q. The boss will see it in the Skills tab to promote or reject.",
			name,
		),
	})
	return string(out), nil
}

// ── skill_optimize ──────────────────────────────────────────────────────────

type skillOptimizeTool struct {
	pool    *pgxpool.Pool
	drafter SkillToolsDrafter
}

func (t *skillOptimizeTool) Name() string { return "skill_optimize" }
func (t *skillOptimizeTool) Description() string {
	return "Suggest an update to an existing skill. Call when you've noticed " +
		"that the current SKILL.md is missing a step, has stale info, or could be " +
		"sharper based on this session. There is ONE active draft per parent_skill: " +
		"if a pending candidate already exists, your new body is MERGED into the " +
		"existing draft (occurrences accumulate, conflicts surface as warnings). " +
		"Before you call this for an existing skill, use `skill_proposal_get` to " +
		"see what's already pending so you can produce a body that incorporates " +
		"BOTH your new change AND the prior pending edits. Returns the proposal " +
		"id, revision number, and any conflicts the merge flagged."
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

	// Route through UpsertCandidate so the new body MERGES into the
	// existing open candidate (if any) instead of stacking another row.
	// One active draft per parent_skill, enforced by migration 039's
	// partial unique index. Conflicts the merge flags come back in the
	// result and bubble up to the boss via the chat card.
	res, err := proposals.UpsertCandidate(ctx, t.pool, t.drafter, slog.Default(), proposals.CandidateDraft{
		Name:             parent + "-update",
		ParentSkill:      parent,
		Description:      "Updated SKILL.md for " + parent,
		Reasoning:        reasoning,
		SkillMD:          skillMD,
		RiskLevel:        "low",
		Importance:       inferSkillImportance(parent, "Updated SKILL.md for "+parent, reasoning, skillMD),
		ImportanceReason: inferSkillImportanceReason(parent, "Updated SKILL.md for "+parent, reasoning, skillMD),
		Source:           "agent",
	})
	if err != nil {
		return "", fmt.Errorf("upsert proposal: %w", err)
	}

	msg := fmt.Sprintf("Proposed update for skill %q (v%d of pending draft). The boss reviews + applies in the Skills tab.",
		parent, res.Revision)
	if !res.IsNew {
		msg = fmt.Sprintf("Merged update into the pending draft for skill %q (now v%d, %d total changes pending).",
			parent, res.Revision, res.Revision)
	}
	if len(res.Conflicts) > 0 {
		msg += fmt.Sprintf(" %d conflict(s) flagged for boss review.", len(res.Conflicts))
	}

	out, _ := json.Marshal(map[string]any{
		"status":       "proposed",
		"id":           res.ID,
		"parent_skill": parent,
		"revision":     res.Revision,
		"is_new":       res.IsNew,
		"conflicts":    res.Conflicts,
		"message":      msg,
	})
	return string(out), nil
}

// ── skill_proposal_get ──────────────────────────────────────────────────────

// skillProposalGetTool returns the current open candidate (if any) for a
// parent_skill. Read-only. The agent calls this BEFORE skill_optimize to
// see what's already pending so the new body it produces can incorporate
// both the new change AND the prior pending edits - that's how a single
// draft accumulates instead of getting overwritten on every call.
type skillProposalGetTool struct {
	pool *pgxpool.Pool
}

func (t *skillProposalGetTool) Name() string   { return "skill_proposal_get" }
func (t *skillProposalGetTool) ReadOnly() bool { return true }
func (t *skillProposalGetTool) Description() string {
	return "Return the currently-open candidate proposal for a parent_skill, if " +
		"any. Use BEFORE calling skill_optimize on an existing skill so you can " +
		"see the pending body + changes_log and produce a new body that " +
		"incorporates both your new change AND the prior pending edits. Returns " +
		"the proposal id, revision, current body, changes_log, and any " +
		"already-detected conflicts. Returns null when no draft is pending."
}
func (t *skillProposalGetTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"parent_skill": map[string]any{
				"type":        "string",
				"description": "Name of the existing skill to look up the pending draft for.",
			},
		},
		"required": []string{"parent_skill"},
	}
}
func (t *skillProposalGetTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	parent, _ := input["parent_skill"].(string)
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "", errors.New("parent_skill is required")
	}
	cand, found, err := proposals.GetOpenCandidate(ctx, t.pool, parent)
	if err != nil {
		return "", fmt.Errorf("get open candidate: %w", err)
	}
	if !found {
		b, _ := json.Marshal(map[string]any{"open_candidate": nil})
		return string(b), nil
	}
	b, _ := json.Marshal(map[string]any{
		"open_candidate": map[string]any{
			"id":                cand.ID,
			"name":              cand.Name,
			"revision":          cand.Revision,
			"description":       cand.Description,
			"reasoning":         cand.Reasoning,
			"risk_level":        cand.RiskLevel,
			"importance":        cand.Importance,
			"importance_reason": cand.ImportanceReason,
			"skill_md":          cand.SkillMD,
			"changes_log":       cand.ChangesLog,
		},
	})
	return string(b), nil
}

func intFrom(in map[string]any, key string) int {
	if v, ok := in[key].(float64); ok {
		return int(v)
	}
	if v, ok := in[key].(int); ok {
		return v
	}
	return 0
}

func inferSkillImportance(name, description, reasoning, body string) int {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case skillHasAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill", "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model", "connector identit"):
		return 95
	case skillHasAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return 85
	case skillHasAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return 65
	default:
		return 60
	}
}

func inferSkillImportanceReason(name, description, reasoning, body string) string {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case skillHasAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill"):
		return "Core self-improvement loop: turns failures, drift, or repeated patterns into better skills."
	case skillHasAny(hay, "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model"):
		return "Core cognition loop: improves memory, reflection, or learning behavior across turns."
	case skillHasAny(hay, "connector identit", "account identit"):
		return "Core tool reliability loop: keeps connected accounts understandable and routable."
	case skillHasAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return "Executive-assistant automation: notices and surfaces work without waiting for a prompt."
	case skillHasAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return "Useful coding/build capability, but not part of the agent cognition core."
	default:
		return "General reusable skill."
	}
}

func skillHasAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
