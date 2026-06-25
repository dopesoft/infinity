package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/proposals"
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

ONE FLEXIBLE SKILL PER CAPABILITY, NOT MANY NARROW ONES. Before drafting, assume a skill for this capability domain probably already exists. The default outcome is to EXTEND that skill with a parameter or branch, not to mint a new one. Only propose a brand-new skill when no existing skill plausibly covers the capability at all. Build variation INTO a skill — handle N accounts, sources, or query windows with parameters and conditional steps — rather than spawning a sibling skill per variation. A capability that fragmented into many tight skills (e.g. "gmail-triage", "load-then-sweep-gmail", "scheduled-gmail-followup-triage" for one inbox-triage job) is a bug, not coverage. Name the skill for the broad capability, never for the specific run. (A deterministic gate may still re-route your draft into the canonical skill; design for that — write the body as a general recipe.)

JUDGMENT ONLY — A MECHANIC IS NOT A SKILL. A skill is a recipe for a JUDGMENT call (which / whether / what / how-much). If the lesson is a deterministic MECHANIC — "always do X", "never do Y", "call A before B", "retry once then escalate", "verify before reporting", "re-anchor on state after an interruption" — it belongs in CODE (a tool, a gate, a contract), NOT a droppable sentence in a skill the model can forget. Do NOT draft a skill whose whole point is a mechanic. When the session's reusable lesson is a mechanic, DECLINE (return the empty/declined JSON below); the durable fix is a code change, not a skill.

DO NOT RE-PROPOSE MACHINERY THAT ALREADY EXISTS AS CODE. The system ALREADY has, in Go: self-heal (a failed turn auto-injects a fix pass), stranded-turn / interrupted-run recovery (boot sweep + finalize), plan management + verification (plan_get/plan_update/plan_verify), commit/push/deploy verification in the nightly self-improve loop, and run-outcome surfacing ("Surfaced by Jarvis"). A "resume a stalled run and verify the commit landed", "re-anchor on the plan before updating", "retry the browser then escalate" recipe is NOT a new capability — it is describing machinery that already runs. DECLINE these. Only a genuinely NEW judgment capability (a new analysis, a new external workflow, a new decision the system can't already make) is worth a skill.

Return ONLY a JSON object in this exact shape - no commentary, no code fences:

{
  "name": "lowercase_underscored_name",
  "description": "<=120 chars, what the skill does",
  "reasoning": "1-2 sentences: why this session crystallizes into a reusable skill",
  "risk_level": "low|medium|high|critical",
  "importance": 0-100,
  "importance_reason": "one sentence explaining strategic value",
  "skill_md": "---\nname: <name>\nversion: '0.1.0'\ndescription: <description>\ntrigger_phrases: ['phrase1', 'phrase2']\ninputs: []\noutputs: []\nrisk_level: <risk>\nnetwork_egress: 'none'\nconfidence: 0.5\n---\n\n# <Title>\n\n## When to use\n\n## Steps\n\n1. ...\n2. ...\n\n## Notes"
}

If the session is a one-off chat, debugging digression, or anything not worth crystallizing, return:
{"name":"","description":"","reasoning":"not a generalizable procedure","risk_level":"low","importance":0,"importance_reason":"","skill_md":""}

Risk level is execution danger, not value:
- low = prompt-only recipe; can still be strategically fundamental
- medium/high/critical = writes files, calls paid/external APIs, sends messages, changes external state, or needs stricter sandboxing

Importance is strategic value:
- 90-100 = core AGI loop: memory grounding, self-improvement, proactivity, tool reliability, executive open-loop tracking
- 70-89 = useful recurring executive/coding workflow
- 40-69 = convenience or narrow workflow

Never invent capabilities. The skill must reflect what actually happened.`

type draftResult struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	Reasoning        string `json:"reasoning"`
	RiskLevel        string `json:"risk_level"`
	Importance       int    `json:"importance"`
	ImportanceReason string `json:"importance_reason"`
	SkillMD          string `json:"skill_md"`
}

func (m *Manager) draftAndStoreSkill(ctx context.Context, sessionID string, stats sessionStats) error {
	transcript := buildTranscript(stats)
	if transcript == "" {
		return nil
	}

	model := envOr("LLM_SUMMARIZE_MODEL", "")
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
	if draft.Importance <= 0 {
		draft.Importance = inferProposalImportance(draft.Name, draft.Description, draft.Reasoning, draft.SkillMD)
	}
	if strings.TrimSpace(draft.ImportanceReason) == "" {
		draft.ImportanceReason = inferProposalImportanceReason(draft.Name, draft.Description, draft.Reasoning, draft.SkillMD)
	}

	// Dedup-before-create: the session extractor is a second skill-create path
	// (alongside the agent's skill_propose), so it must consult the same gate or
	// it re-mints "session_pattern_*" near-duplicates of skills that already
	// exist. If the draft duplicates an active skill, route it through the merge
	// machinery (one accumulating draft per parent_skill) instead of a new row.
	if match := proposals.FindDuplicateSkill(ctx, m.pool, m.llm, draft.Name, draft.Description); match != "" {
		_, err := proposals.UpsertCandidate(ctx, m.pool, m.llm, slog.Default(), proposals.CandidateDraft{
			Name:             match + "-update",
			ParentSkill:      match,
			Description:      truncate(draft.Description, 200),
			Reasoning:        "Auto-routed from session extractor (duplicate of " + match + "): " + truncate(draft.Reasoning, 400),
			SkillMD:          draft.SkillMD,
			RiskLevel:        strings.ToLower(draft.RiskLevel),
			Importance:       draft.Importance,
			ImportanceReason: draft.ImportanceReason,
			Source:           "auto_evolved",
		})
		if err == nil {
			return nil // merged into existing skill's draft; no standalone row, no verify pass
		}
		// On merge failure, fall through to a standalone insert rather than
		// dropping the signal.
	}

	var proposalID string
	err = m.pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, importance, importance_reason, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'candidate')
		RETURNING id::text
	`, draft.Name, truncate(draft.Description, 200), truncate(draft.Reasoning, 500),
		draft.SkillMD, strings.ToLower(draft.RiskLevel), draft.Importance, draft.ImportanceReason).Scan(&proposalID)
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

// insertProposalStub is the no-LLM fallback: we can't draft a real SKILL.md
// without the model, but we MUST NOT surface a content-less "used N tools"
// mystery. A tool count is not a reason — the boss's actual request is. So the
// stub leads with what the session was trying to do (the first user prompt) and
// names the tools it used, and says plainly that drafting is pending the model.
// That makes the card answer "what pattern?" instead of demanding the boss go
// spelunking. The real reasoning still arrives when the LLM drafts; this is the
// honest placeholder, not a fake conclusion.
func (m *Manager) insertProposalStub(ctx context.Context, sessionID string, stats sessionStats) error {
	ask := firstUserPrompt(stats)
	tools := sortedToolNames(stats.DistinctTools, 8)

	desc := "Repeatable task — pending skill drafting"
	if ask != "" {
		desc = truncate("Repeatable task: "+oneLine(ask), 120)
	}

	var reason strings.Builder
	if ask != "" {
		fmt.Fprintf(&reason, "What you were doing: %s\n", truncate(oneLine(ask), 300))
	}
	if len(tools) > 0 {
		fmt.Fprintf(&reason, "Tools used: %s\n", strings.Join(tools, ", "))
	}
	fmt.Fprintf(&reason, "(%d tools · %.0fs · %d failures.) The model was offline, so Jarvis flagged the session but couldn't draft the skill yet — review to capture it or dismiss.",
		len(stats.DistinctTools), stats.DurationSec, stats.ToolFailures)

	_, err := m.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, importance, importance_reason, status)
		VALUES ($1, $2, $3, $4, 'low', 70, 'Repeatable task flagged from a session; review for reusable automation value.', 'candidate')
	`,
		fmt.Sprintf("session_pattern_%d", time.Now().Unix()),
		desc,
		truncate(reason.String(), 500),
		"")
	return err
}

// firstUserPrompt returns the session's opening ask — the intent that makes a
// session interesting, vs the tool count that doesn't.
func firstUserPrompt(stats sessionStats) string {
	for _, p := range stats.UserPrompts {
		if s := strings.TrimSpace(p); s != "" {
			return s
		}
	}
	// Fall back to the first UserPromptSubmit observation if UserPrompts wasn't
	// populated for some reason.
	for _, o := range stats.Observations {
		if o.Hook == "UserPromptSubmit" {
			if s := strings.TrimSpace(o.RawText); s != "" {
				return s
			}
		}
	}
	return ""
}

// sortedToolNames returns the distinct tool names (stable order), capped, with a
// "+N more" tail so the card names what was actually used instead of a number.
func sortedToolNames(m map[string]int, cap int) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	if cap > 0 && len(names) > cap {
		extra := len(names) - cap
		names = append(names[:cap], fmt.Sprintf("+%d more", extra))
	}
	return names
}

// oneLine collapses whitespace/newlines so a multi-line prompt reads as one line
// on a card.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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

func inferProposalImportance(name, description, reasoning, body string) int {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case hasAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill", "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model", "connector identit"):
		return 95
	case hasAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return 85
	case hasAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return 65
	default:
		return 60
	}
}

func inferProposalImportanceReason(name, description, reasoning, body string) string {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case hasAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill"):
		return "Core self-improvement loop: turns failures, drift, or repeated patterns into better skills."
	case hasAny(hay, "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model"):
		return "Core cognition loop: improves memory, reflection, or learning behavior across turns."
	case hasAny(hay, "connector identit", "account identit"):
		return "Core tool reliability loop: keeps connected accounts understandable and routable."
	case hasAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return "Executive-assistant automation: notices and surfaces work without waiting for a prompt."
	case hasAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return "Useful coding/build capability, but not part of the agent cognition core."
	default:
		return "General reusable skill."
	}
}

func hasAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
