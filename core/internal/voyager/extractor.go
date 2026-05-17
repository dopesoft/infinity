package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
)

// Heuristic thresholds for a session being "skill-worthy". Tuned against the
// Hermes paper's pattern (≥3 tools, error-free, non-trivial duration). Bias
// toward fewer false positives - every accepted candidate eats a Haiku turn.
const (
	minDistinctTools = 3
	minDurationSec   = 30
	maxFailureRatio  = 0.34 // <34% of tool calls allowed to fail
	extractorMaxObs  = 50
)

// OnSessionEnd is the hook handler. Wire as:
//
//	pipeline.RegisterFunc("voyager.extract", m.OnSessionEnd, hooks.SessionEnd)
//
// Synchronously runs the heuristic. If it passes, dispatches the Haiku draft
// in a goroutine so the session-end path stays snappy.
func (m *Manager) OnSessionEnd(ctx context.Context, ev hooks.Event) error {
	if !m.Enabled() {
		return nil
	}

	stats, err := m.collectSessionStats(ctx, ev.SessionID)
	if err != nil {
		return err
	}
	if !stats.qualifies() {
		return nil
	}
	if m.llm == nil {
		// Discovery-only mode: log a row noting we'd have extracted, but skip
		// the Haiku draft. Keeps signal in the proposals view.
		return m.insertProposalStub(ctx, ev.SessionID, stats)
	}

	go func(sessionID string, st sessionStats) {
		bg, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := m.draftAndStoreSkill(bg, sessionID, st); err != nil {
			fmt.Printf("[voyager] extract %s: %v\n", sessionID, err)
		}
	}(ev.SessionID, stats)

	return nil
}

type sessionStats struct {
	StartedAt      time.Time
	EndedAt        time.Time
	DurationSec    float64
	Observations   []extractorObs
	ToolCalls      int
	ToolFailures   int
	DistinctTools  map[string]int
	UserPrompts    []string
	AssistantTurns []string
}

type extractorObs struct {
	Hook    string
	RawText string
	At      time.Time
}

func (s sessionStats) qualifies() bool {
	if s.DurationSec < minDurationSec {
		return false
	}
	if len(s.DistinctTools) < minDistinctTools {
		return false
	}
	if s.ToolCalls > 0 {
		ratio := float64(s.ToolFailures) / float64(s.ToolCalls)
		if ratio > maxFailureRatio {
			return false
		}
	}
	return true
}

func (m *Manager) collectSessionStats(ctx context.Context, sessionID string) (sessionStats, error) {
	var stats sessionStats
	stats.DistinctTools = map[string]int{}
	if sessionID == "" || m.pool == nil {
		return stats, nil
	}

	rows, err := m.pool.Query(ctx, `
		SELECT hook_name, COALESCE(raw_text, ''), payload, created_at
		FROM mem_observations
		WHERE session_id = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, sessionID, extractorMaxObs)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var hook, raw string
		var payload []byte
		var at time.Time
		if err := rows.Scan(&hook, &raw, &payload, &at); err != nil {
			return stats, err
		}
		if stats.StartedAt.IsZero() {
			stats.StartedAt = at
		}
		stats.EndedAt = at
		stats.Observations = append(stats.Observations, extractorObs{Hook: hook, RawText: raw, At: at})

		switch hook {
		case "PreToolUse":
			stats.ToolCalls++
			if name := extractToolName(payload); name != "" {
				stats.DistinctTools[name]++
			}
		case "PostToolUseFailure":
			stats.ToolFailures++
		case "UserPromptSubmit":
			if raw != "" {
				stats.UserPrompts = append(stats.UserPrompts, raw)
			}
		case "TaskCompleted":
			if raw != "" {
				stats.AssistantTurns = append(stats.AssistantTurns, raw)
			}
		}
	}
	stats.DurationSec = stats.EndedAt.Sub(stats.StartedAt).Seconds()
	return stats, rows.Err()
}

func extractToolName(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	if v, ok := p["name"].(string); ok {
		return v
	}
	return ""
}

const draftSystem = `You convert a successful agent session into a reusable skill.

You'll get a session transcript with the user's prompts, the assistant's replies, and the tools the assistant called. Decide whether the session captured a generalizable procedure (not a one-off question), and if so produce a SKILL.md.

Return ONLY a JSON object in this exact shape - no commentary, no code fences:

{
  "name": "lowercase_underscored_name",
  "description": "<=120 chars, what the skill does",
  "reasoning": "1-2 sentences: why this session crystallizes into a reusable skill",
  "risk_level": "low|medium|high|critical",
  "skill_md": "---\nname: <name>\nversion: '0.1.0'\ndescription: <description>\ntrigger_phrases: ['phrase1', 'phrase2']\ninputs: []\noutputs: []\nrisk_level: <risk>\nnetwork_egress: 'none'\nconfidence: 0.5\n---\n\n# <Title>\n\n## When to use\n\n## Steps\n\n1. ...\n2. ...\n\n## Notes"
}

If the session is a one-off chat, debugging digression, or anything not worth crystallizing, return:
{"name":"","description":"","reasoning":"not a generalizable procedure","risk_level":"low","skill_md":""}

Never invent capabilities. The skill must reflect what actually happened.`

type draftResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Reasoning   string `json:"reasoning"`
	RiskLevel   string `json:"risk_level"`
	SkillMD     string `json:"skill_md"`
}

func (m *Manager) draftAndStoreSkill(ctx context.Context, sessionID string, stats sessionStats) error {
	transcript := buildTranscript(stats)
	if transcript == "" {
		return nil
	}

	model := envOr("LLM_SUMMARIZE_MODEL", "claude-haiku-4-5-20251001")
	raw, err := m.llm.Draft(ctx, model, draftSystem, transcript, 1500)
	if err != nil {
		return err
	}
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil
	}

	var draft draftResult
	if err := json.Unmarshal([]byte(cleaned), &draft); err != nil {
		return fmt.Errorf("draft parse: %w", err)
	}
	if strings.TrimSpace(draft.Name) == "" || strings.TrimSpace(draft.SkillMD) == "" {
		return nil // model declined; not skill-worthy
	}
	if draft.RiskLevel == "" {
		draft.RiskLevel = "low"
	}

	var proposalID string
	err = m.pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, status)
		VALUES ($1, $2, $3, $4, $5, 'candidate')
		RETURNING id::text
	`, draft.Name, truncate(draft.Description, 200), truncate(draft.Reasoning, 500),
		draft.SkillMD, strings.ToLower(draft.RiskLevel)).Scan(&proposalID)
	if err != nil {
		return fmt.Errorf("insert proposal: %w", err)
	}

	// Verify in background - generates synthetic tests + auto-promote rule.
	go func(id, name string) {
		bg, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := m.verifyProposal(bg, id, name); err != nil {
			fmt.Printf("[voyager] verify %s: %v\n", name, err)
		}
	}(proposalID, draft.Name)
	return nil
}

func (m *Manager) insertProposalStub(ctx context.Context, sessionID string, stats sessionStats) error {
	_, err := m.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, status)
		VALUES ($1, $2, $3, $4, 'low', 'candidate')
	`,
		fmt.Sprintf("session_pattern_%d", time.Now().Unix()),
		"Skill-worthy session detected (LLM unavailable for drafting)",
		fmt.Sprintf("Session %s used %d distinct tools across %.0fs with %d failures.",
			sessionID, len(stats.DistinctTools), stats.DurationSec, stats.ToolFailures),
		"")
	return err
}

func buildTranscript(stats sessionStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session duration: %.0fs · tool calls: %d · failures: %d · distinct tools: %d\n\n",
		stats.DurationSec, stats.ToolCalls, stats.ToolFailures, len(stats.DistinctTools))
	tools := []string{}
	for k := range stats.DistinctTools {
		tools = append(tools, k)
	}
	if len(tools) > 0 {
		fmt.Fprintf(&b, "Tools used: %s\n\n", strings.Join(tools, ", "))
	}
	for _, obs := range stats.Observations {
		switch obs.Hook {
		case "UserPromptSubmit":
			fmt.Fprintf(&b, "USER: %s\n", truncate(obs.RawText, 600))
		case "TaskCompleted":
			fmt.Fprintf(&b, "ASSISTANT: %s\n", truncate(obs.RawText, 800))
		case "PreToolUse":
			fmt.Fprintf(&b, "TOOL CALL: %s\n", truncate(obs.RawText, 200))
		case "PostToolUseFailure":
			fmt.Fprintf(&b, "TOOL FAILED: %s\n", truncate(obs.RawText, 200))
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
