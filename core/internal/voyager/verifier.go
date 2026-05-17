package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Verifier policy.
//
// We can't safely "execute" a freshly-drafted skill because the agent might
// invoke real tools (filesystem, http, etc) before we've decided to trust it.
// So the verifier is gentler than the Hermes "ship and pray" pattern but
// stricter than just storing the candidate.
//
// Two paths:
//
//  1. Generate synthetic test cases via Haiku - concrete inputs the skill
//     should handle. These get persisted to mem_skill_tests for later run.
//
//  2. Auto-promote rule. Risk=low + body has no impl_path/scripts/network
//     egress = pure prompt skill. Those are safe by construction (the agent
//     just reads the instructions and acts as it would have anyway), so we
//     promote them to status='active' immediately and mark the proposal
//     status='promoted'.
//
//     Anything with implementation, network access, or risk > low stays as
//     a candidate awaiting human review in the Memory tab.

const synthSystem = `You generate synthetic test cases for an agent skill.

Given the skill's SKILL.md, propose 3 concrete inputs the skill should handle gracefully. Return ONLY a JSON array of objects with shape:

[
  {"description": "what this case checks", "inputs": {"key": "value"}, "expected": "what should happen / what answer is acceptable"}
]

Inputs may be an empty object if the skill takes none. Expected may be a brief description, not literal output. If you can't propose meaningful tests, return [].`

type synthTest struct {
	Description string                 `json:"description"`
	Inputs      map[string]any         `json:"inputs"`
	Expected    string                 `json:"expected"`
}

// verifyProposal generates synthetic tests for the given proposal and applies
// the auto-promote rule. Run from extractor as a goroutine.
func (m *Manager) verifyProposal(ctx context.Context, proposalID, name string) error {
	if !m.Enabled() || m.pool == nil {
		return nil
	}

	var skillMD, riskLevel string
	err := m.pool.QueryRow(ctx, `
		SELECT skill_md, risk_level
		FROM mem_skill_proposals
		WHERE id = $1
	`, proposalID).Scan(&skillMD, &riskLevel)
	if err != nil {
		return err
	}

	// Synthetic test generation is best-effort.
	if m.llm != nil && skillMD != "" {
		_ = m.generateSyntheticTests(ctx, name, skillMD)
	}

	// Auto-promote eligibility check.
	if isAutoPromotable(skillMD, riskLevel) {
		if err := m.Decide(ctx, proposalID, "promoted"); err != nil {
			return fmt.Errorf("auto-promote: %w", err)
		}
		fmt.Printf("[voyager] auto-promoted candidate skill: %s\n", name)
	}
	return nil
}

func (m *Manager) generateSyntheticTests(ctx context.Context, skillName, skillMD string) error {
	model := envOr("LLM_SUMMARIZE_MODEL", "claude-haiku-4-5-20251001")
	raw, err := m.llm.Draft(ctx, model, synthSystem, skillMD, 1000)
	if err != nil {
		return err
	}
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "[]" {
		return nil
	}

	var tests []synthTest
	if err := json.Unmarshal([]byte(cleaned), &tests); err != nil {
		return err
	}

	for _, t := range tests {
		inputs, _ := json.Marshal(t.Inputs)
		_, _ = m.pool.Exec(ctx, `
			INSERT INTO mem_skill_tests
			  (skill_name, description, inputs, expected, source)
			VALUES ($1, $2, $3::jsonb, $4, 'synthetic')
			ON CONFLICT DO NOTHING
		`, skillName, t.Description, string(inputs), t.Expected)
	}
	return nil
}

// isAutoPromotable says: pure-prompt low-risk skills with no network egress
// and no implementation file can ship straight to active. Everything else
// waits for a human (or future automated runner).
func isAutoPromotable(skillMD, riskLevel string) bool {
	if !strings.EqualFold(strings.TrimSpace(riskLevel), "low") {
		return false
	}
	body := strings.ToLower(skillMD)
	if strings.Contains(body, "impl_path:") {
		return false
	}
	if strings.Contains(body, "implementation:") {
		return false
	}
	// network_egress: 'none' is fine; any list value blocks auto-promote.
	if !containsAny(body, []string{"network_egress: 'none'", "network_egress: \"none\"", "network_egress: none", "network_egress: []"}) {
		return false
	}
	return true
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
