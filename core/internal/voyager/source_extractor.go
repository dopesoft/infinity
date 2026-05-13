package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
)

// File-fight thresholds. Tuned to bias toward false negatives — we only
// want to propose source changes when the boss visibly struggled with the
// same file in one session. A single clean edit is normal; three edits
// punctuated by failures is a smell.
const (
	minEditsPerFile       = 3    // same file edited at least this many times
	minBashFailures       = 1    // ≥1 PostToolUseFailure during the session
	sourceExtractorMaxObs = 200  // cap session scan for cost
	maxFilesPerSession    = 3    // propose at most this many distinct refactors per session
)

// OnSessionEndSource is the source-extraction hook. Wire as:
//
//	pipeline.RegisterFunc("voyager.source_extract", m.OnSessionEndSource, hooks.SessionEnd)
//
// Mirrors OnSessionEnd's contract: synchronous heuristic + async Haiku draft.
// Failures log but never bubble back to the loop — capture must not block the
// session-end path.
func (m *Manager) OnSessionEndSource(ctx context.Context, ev hooks.Event) error {
	if !m.Enabled() {
		return nil
	}

	stats, err := m.collectFileFightStats(ctx, ev.SessionID)
	if err != nil {
		return err
	}
	hot := stats.hotFiles()
	if len(hot) == 0 {
		return nil
	}

	// Async draft. SessionEnd ought to return promptly — Haiku turns can
	// run for 30+ seconds on the proposal prompt below.
	go func(sessionID string, paths []string, st fileFightStats) {
		bg, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		for _, p := range paths {
			if err := m.draftAndStoreCodeProposal(bg, sessionID, p, st); err != nil {
				fmt.Printf("[voyager] source_extract %s %s: %v\n", sessionID, p, err)
			}
		}
	}(ev.SessionID, hot, stats)

	return nil
}

type fileFightStats struct {
	SessionID      string
	StartedAt      time.Time
	EndedAt        time.Time
	DurationSec    float64
	EditsByFile    map[string]int      // file_path → edit count (edit+write)
	FailuresByFile map[string]int      // file_path → tool-failure count when this file was the target
	BashCommands   []string            // shell commands the agent ran (truncated)
	BashFailures   int                 // total PostToolUseFailure where tool was claude_code__bash
	UserPrompts    []string
	AssistantTurns []string
}

// hotFiles returns the file paths that crossed the "fight" threshold,
// capped at maxFilesPerSession. Sorted by edit count descending so the
// most-touched files are proposed first.
func (s fileFightStats) hotFiles() []string {
	type fileScore struct {
		path  string
		edits int
		fails int
	}
	scored := make([]fileScore, 0, len(s.EditsByFile))
	for path, edits := range s.EditsByFile {
		if edits < minEditsPerFile {
			continue
		}
		// A file fight requires SOME signal of struggle, not just multiple
		// touches. Either a failure landed against this file specifically,
		// OR the session as a whole had bash failures (typecheck/test
		// failures with no specific file attribution).
		fails := s.FailuresByFile[path]
		if fails == 0 && s.BashFailures < minBashFailures {
			continue
		}
		scored = append(scored, fileScore{path: path, edits: edits, fails: fails})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].fails != scored[j].fails {
			return scored[i].fails > scored[j].fails
		}
		return scored[i].edits > scored[j].edits
	})
	if len(scored) > maxFilesPerSession {
		scored = scored[:maxFilesPerSession]
	}
	out := make([]string, len(scored))
	for i, s := range scored {
		out[i] = s.path
	}
	return out
}

func (m *Manager) collectFileFightStats(ctx context.Context, sessionID string) (fileFightStats, error) {
	stats := fileFightStats{
		SessionID:      sessionID,
		EditsByFile:    map[string]int{},
		FailuresByFile: map[string]int{},
	}
	if sessionID == "" || m.pool == nil {
		return stats, nil
	}

	rows, err := m.pool.Query(ctx, `
		SELECT hook_name, COALESCE(raw_text, ''), payload, created_at
		FROM mem_observations
		WHERE session_id = $1
		ORDER BY created_at ASC
		LIMIT $2
	`, sessionID, sourceExtractorMaxObs)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	// Track the most recent tool name + file path so a PostToolUseFailure
	// can be attributed back to the file the agent was editing.
	var lastFile, lastTool string

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

		switch hook {
		case "PreToolUse":
			toolName, input := parseToolCall(payload)
			lastTool = toolName
			if isClaudeCodeEdit(toolName) {
				path := extractFilePath(input)
				if path != "" {
					stats.EditsByFile[path]++
					lastFile = path
				}
			} else if isClaudeCodeBash(toolName) {
				lastFile = ""
				if cmd := extractBashCommand(input); cmd != "" {
					stats.BashCommands = append(stats.BashCommands, truncate(cmd, 240))
				}
			} else {
				lastFile = ""
			}
		case "PostToolUseFailure":
			if isClaudeCodeBash(lastTool) {
				stats.BashFailures++
			}
			if lastFile != "" {
				stats.FailuresByFile[lastFile]++
			}
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

func parseToolCall(payload []byte) (string, map[string]any) {
	if len(payload) == 0 {
		return "", nil
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", nil
	}
	name, _ := p["name"].(string)
	input, _ := p["input"].(map[string]any)
	return name, input
}

func isClaudeCodeEdit(name string) bool {
	n := strings.ToLower(name)
	return n == "claude_code__edit" || n == "claude_code__write"
}

func isClaudeCodeBash(name string) bool {
	return strings.ToLower(name) == "claude_code__bash"
}

// extractFilePath pulls the target file out of a claude_code__edit /
// claude_code__write tool input. Anthropic's Claude Code MCP tools use
// `file_path` consistently, but be defensive — older variants used `path`.
func extractFilePath(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filename"} {
		if v, ok := input[key].(string); ok {
			s := strings.TrimSpace(v)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func extractBashCommand(input map[string]any) string {
	if input == nil {
		return ""
	}
	if v, ok := input["command"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

const sourceDraftSystem = `You are reviewing a coding session where the user fought with a single source file. Decide whether the file has a real refactoring opportunity worth proposing.

You'll get:
  - The file path
  - Stats: number of edits, failure count, session duration, bash commands run
  - User prompts and assistant replies from the session

Return ONLY a JSON object — no commentary, no code fences:

{
  "title": "<=80 chars, action-oriented: 'Extract X', 'Replace Y with Z', 'Split A into B and C'",
  "rationale": "1-3 sentences: WHAT symptom the session showed, WHY it suggests this file is fragile, and WHAT the proposed change should achieve",
  "proposed_change": "A concrete plan as a bullet list or short prose. Reference specific functions/sections by name. Keep it under 600 chars — this is a sketch the agent will expand at apply-time, not a finished diff.",
  "risk_level": "low|medium|high|critical"
}

If the session does NOT actually indicate a refactor opportunity (e.g. the boss was just exploring, or the failures were unrelated to file quality), return:
  {"title":"","rationale":"not a real refactor signal","proposed_change":"","risk_level":"low"}

Risk levels:
  low      — pure rename/extract within the file, no behavior change
  medium   — split function, change signatures, touch callers in 1-2 files
  high     — schema migration, breaking API change, behavior shift
  critical — security, data loss, or wide-blast-radius change

Never invent symptoms. Anchor every claim to evidence from the transcript.`

type sourceDraftResult struct {
	Title          string `json:"title"`
	Rationale      string `json:"rationale"`
	ProposedChange string `json:"proposed_change"`
	RiskLevel      string `json:"risk_level"`
}

func (m *Manager) draftAndStoreCodeProposal(ctx context.Context, sessionID, filePath string, stats fileFightStats) error {
	if m.pool == nil {
		return nil
	}

	// LLM is optional. Without one, store a stub proposal that still
	// surfaces the signal so the boss can investigate manually.
	if m.llm == nil {
		return m.insertCodeProposalStub(ctx, sessionID, filePath, stats)
	}

	transcript := buildSourceTranscript(filePath, stats)
	if transcript == "" {
		return nil
	}

	model := envOr("LLM_SUMMARIZE_MODEL", "claude-haiku-4-5-20251001")
	raw, err := m.llm.Draft(ctx, model, sourceDraftSystem, transcript, 1500)
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

	var draft sourceDraftResult
	if err := json.Unmarshal([]byte(cleaned), &draft); err != nil {
		return fmt.Errorf("source draft parse: %w", err)
	}
	if strings.TrimSpace(draft.Title) == "" {
		return nil // model declined; not a real refactor signal
	}
	risk := strings.ToLower(strings.TrimSpace(draft.RiskLevel))
	switch risk {
	case "low", "medium", "high", "critical":
	default:
		risk = "medium"
	}

	evidence := map[string]any{
		"edit_count":    stats.EditsByFile[filePath],
		"failure_count": stats.FailuresByFile[filePath],
		"bash_failures": stats.BashFailures,
		"duration_sec":  stats.DurationSec,
		"bash_sample":   firstN(stats.BashCommands, 5),
	}
	evJSON, _ := json.Marshal(evidence)

	_, err = m.pool.Exec(ctx, `
		INSERT INTO mem_code_proposals
		  (target_path, title, rationale, proposed_change, evidence, risk_level, status, source_session)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, 'candidate', NULLIF($7,'')::uuid)
	`,
		filePath,
		truncate(draft.Title, 200),
		truncate(draft.Rationale, 1000),
		truncate(draft.ProposedChange, 2000),
		string(evJSON),
		risk,
		sessionID,
	)
	return err
}

func (m *Manager) insertCodeProposalStub(ctx context.Context, sessionID, filePath string, stats fileFightStats) error {
	evidence := map[string]any{
		"edit_count":    stats.EditsByFile[filePath],
		"failure_count": stats.FailuresByFile[filePath],
		"bash_failures": stats.BashFailures,
		"duration_sec":  stats.DurationSec,
	}
	evJSON, _ := json.Marshal(evidence)

	rationale := fmt.Sprintf(
		"%d edits and %d related failures across %.0fs. LLM unavailable to draft a concrete refactor — review session and decide manually.",
		stats.EditsByFile[filePath], stats.FailuresByFile[filePath], stats.DurationSec,
	)
	_, err := m.pool.Exec(ctx, `
		INSERT INTO mem_code_proposals
		  (target_path, title, rationale, proposed_change, evidence, risk_level, status, source_session)
		VALUES ($1, $2, $3, '', $4::jsonb, 'medium', 'candidate', NULLIF($5,'')::uuid)
	`,
		filePath,
		"File fought during session — needs review",
		rationale,
		string(evJSON),
		sessionID,
	)
	return err
}

func buildSourceTranscript(filePath string, stats fileFightStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target file: %s\n", filePath)
	fmt.Fprintf(&b, "Edits to this file: %d · failures attributed: %d · session bash failures: %d · duration: %.0fs\n\n",
		stats.EditsByFile[filePath], stats.FailuresByFile[filePath], stats.BashFailures, stats.DurationSec)
	if len(stats.BashCommands) > 0 {
		fmt.Fprintf(&b, "Bash commands run during session (up to 10):\n")
		for _, c := range firstN(stats.BashCommands, 10) {
			fmt.Fprintf(&b, "  $ %s\n", c)
		}
		b.WriteString("\n")
	}
	if len(stats.UserPrompts) > 0 {
		b.WriteString("User prompts:\n")
		for _, p := range firstN(stats.UserPrompts, 8) {
			fmt.Fprintf(&b, "  - %s\n", truncate(p, 400))
		}
		b.WriteString("\n")
	}
	if len(stats.AssistantTurns) > 0 {
		b.WriteString("Assistant replies (most recent first):\n")
		// Newest replies are most informative about the final state of the
		// struggle, so reverse before truncation.
		recent := stats.AssistantTurns
		if len(recent) > 6 {
			recent = recent[len(recent)-6:]
		}
		for i := len(recent) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "  - %s\n", truncate(recent[i], 600))
		}
	}
	return b.String()
}

func firstN[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

// CodeProposalDTO is the wire shape for mem_code_proposals.
type CodeProposalDTO struct {
	ID             string          `json:"id"`
	TargetPath     string          `json:"target_path"`
	Title          string          `json:"title"`
	Rationale      string          `json:"rationale"`
	ProposedChange string          `json:"proposed_change"`
	Evidence       json.RawMessage `json:"evidence"`
	RiskLevel      string          `json:"risk_level"`
	Status         string          `json:"status"`
	SourceSession  string          `json:"source_session,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	DecidedAt      *time.Time      `json:"decided_at,omitempty"`
	DecisionNote   string          `json:"decision_note,omitempty"`
}

// ListCodeProposals returns code-refactor proposals filtered by status.
// Empty status = all. Limit is capped at 200.
func (m *Manager) ListCodeProposals(ctx context.Context, status string, limit int) ([]CodeProposalDTO, error) {
	if m == nil || m.pool == nil {
		return []CodeProposalDTO{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT id::text,
		       COALESCE(target_path, ''),
		       title,
		       COALESCE(rationale, ''),
		       COALESCE(proposed_change, ''),
		       COALESCE(evidence::text, '{}'),
		       risk_level,
		       status,
		       COALESCE(source_session::text, ''),
		       created_at, decided_at,
		       COALESCE(decision_note, '')
		FROM mem_code_proposals
	`
	args := []any{}
	if status != "" {
		q += ` WHERE status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)

	rows, err := m.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodeProposalDTO{}
	for rows.Next() {
		var p CodeProposalDTO
		var ev string
		var decided *time.Time
		if err := rows.Scan(&p.ID, &p.TargetPath, &p.Title, &p.Rationale, &p.ProposedChange,
			&ev, &p.RiskLevel, &p.Status, &p.SourceSession, &p.CreatedAt, &decided, &p.DecisionNote); err != nil {
			return nil, err
		}
		p.Evidence = json.RawMessage(ev)
		p.DecidedAt = decided
		out = append(out, p)
	}
	return out, rows.Err()
}

// DecideCodeProposal updates a proposal's status. Unlike skill proposals,
// promoting a code proposal does NOT auto-apply the change — it only marks
// the proposal as approved for the agent to attempt on its next turn. The
// actual edit still flows through ClaudeCodeGate → Trust queue.
//
// Valid decisions: "approved", "rejected", "applied".
func (m *Manager) DecideCodeProposal(ctx context.Context, id, decision, note string) error {
	if m == nil || m.pool == nil {
		return fmt.Errorf("voyager: no database pool")
	}
	switch decision {
	case "approved", "rejected", "applied":
	default:
		return fmt.Errorf("voyager: decision must be approved, rejected, or applied")
	}
	_, err := m.pool.Exec(ctx, `
		UPDATE mem_code_proposals
		SET status = $2, decided_at = NOW(), decision_note = $3
		WHERE id = $1
	`, id, decision, truncate(strings.TrimSpace(note), 1000))
	return err
}
