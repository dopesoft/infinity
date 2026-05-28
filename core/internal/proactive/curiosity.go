package proactive

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CuriosityScan looks for gaps in the agent's knowledge graph and produces
// questions to ask the user. This is the active-learning loop CoALA and
// Generative Agents (Park et al.) describe: the agent doesn't only answer
// questions, it FINDS gaps and surfaces them.
//
// Four signal types:
//  1. low_confidence  - semantic memories whose strength has decayed under
//     0.35 but haven't been forgotten yet.
//  2. contradiction   - unresolved 'contradicts' edges where both memories
//     are still active.
//  3. uncovered_mention - graph nodes referenced repeatedly with no memory
//     providing context.
//  4. high_surprise   - predictions where surprise_score >= 0.8.
//
// Each gap writes a row to mem_curiosity_questions (unique on the open
// question text). The heartbeat surfaces them as findings, and Studio can
// route them to the Trust queue or the chat composer.
type CuriosityScan struct {
	pool *pgxpool.Pool
}

func NewCuriosityScan(pool *pgxpool.Pool) *CuriosityScan {
	return &CuriosityScan{pool: pool}
}

// Run executes one scan. Returns the number of NEW open questions written
// (idempotent on question text, so re-runs don't multiply rows).
func (c *CuriosityScan) Run(ctx context.Context) (int, error) {
	if c == nil || c.pool == nil {
		return 0, nil
	}
	total := 0

	// 1. Low-confidence semantic memories.
	added, err := c.scanLowConfidence(ctx)
	if err == nil {
		total += added
	}

	// 2. Unresolved contradictions.
	added, err = c.scanContradictions(ctx)
	if err == nil {
		total += added
	}

	// 3. Uncovered graph mentions.
	added, err = c.scanUncoveredMentions(ctx)
	if err == nil {
		total += added
	}

	// 4. High-surprise predictions.
	added, err = c.scanHighSurprise(ctx)
	if err == nil {
		total += added
	}

	return total, nil
}

func (c *CuriosityScan) scanLowConfidence(ctx context.Context) (int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id::text, COALESCE(title, ''), COALESCE(content, ''), COALESCE(project, '')
		  FROM mem_memories
		 WHERE tier = 'semantic'
		   AND status = 'active'
		   AND strength < 0.35
		   AND created_at < NOW() - INTERVAL '24 hours'
		   AND COALESCE(project, '') <> '_self'
		 ORDER BY strength ASC, updated_at ASC
		 LIMIT 5
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, title, content, project string
		if err := rows.Scan(&id, &title, &content, &project); err != nil {
			continue
		}
		subject, ok := lowConfidenceQuestionSubject(title, content, project)
		if !ok {
			continue
		}
		question := fmt.Sprintf("Is this still true: %s?", subject)
		rationale := "Semantic memory has decayed below confidence threshold - ask the boss to confirm or retire it."
		if c.insertQuestion(ctx, question, rationale, "low_confidence", []string{id}, 6) {
			n++
		}
	}
	return n, rows.Err()
}

func (c *CuriosityScan) scanContradictions(ctx context.Context) (int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT r.source_id::text, r.target_id::text,
		       COALESCE(s.title, ''), COALESCE(t.title, '')
		  FROM mem_relations r
		  JOIN mem_memories s ON s.id = r.source_id
		  JOIN mem_memories t ON t.id = r.target_id
		 WHERE r.relation_type = 'contradicts'
		   AND s.status = 'active' AND t.status = 'active'
		 ORDER BY r.created_at DESC
		 LIMIT 5
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var srcID, tgtID, srcTitle, tgtTitle string
		if err := rows.Scan(&srcID, &tgtID, &srcTitle, &tgtTitle); err != nil {
			continue
		}
		question := fmt.Sprintf("Two memories disagree - which is right: %q or %q?",
			clipShort(srcTitle, 80), clipShort(tgtTitle, 80))
		rationale := "Both memories are active but a 'contradicts' edge links them - need the boss to resolve."
		if c.insertQuestion(ctx, question, rationale, "contradiction", []string{srcID, tgtID}, 8) {
			n++
		}
	}
	return n, rows.Err()
}

func (c *CuriosityScan) scanUncoveredMentions(ctx context.Context) (int, error) {
	rows, err := c.pool.Query(ctx, `
		WITH counts AS (
			SELECT n.id, n.type, n.name, COUNT(no.observation_id) AS mentions
			  FROM mem_graph_nodes n
			  LEFT JOIN mem_graph_node_observations no ON no.node_id = n.id
			  LEFT JOIN mem_memory_sources ms ON ms.observation_id = no.observation_id
			 WHERE n.stale_flag = FALSE
			 GROUP BY n.id, n.type, n.name
			HAVING COUNT(no.observation_id) >= 3
			   AND COUNT(ms.memory_id) = 0
		)
		SELECT id::text, type, name FROM counts
		 ORDER BY mentions DESC
		 LIMIT 3
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, kind, name string
		if err := rows.Scan(&id, &kind, &name); err != nil {
			continue
		}
		question := fmt.Sprintf("The boss has mentioned %s %q multiple times - what's important about it?", kind, name)
		rationale := "Repeated graph mentions with no derived memory - gap worth filling."
		if c.insertQuestion(ctx, question, rationale, "uncovered_mention", []string{id}, 5) {
			n++
		}
	}
	return n, rows.Err()
}

func (c *CuriosityScan) scanHighSurprise(ctx context.Context) (int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id::text, tool_name, expected, COALESCE(actual, '')
		  FROM mem_predictions
		 WHERE surprise_score >= 0.8
		   AND resolved_at > NOW() - INTERVAL '48 hours'
		 ORDER BY surprise_score DESC, resolved_at DESC
		 LIMIT 3
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, tool, expected, actual string
		if err := rows.Scan(&id, &tool, &expected, &actual); err != nil {
			continue
		}
		if shouldSuppressHighSurpriseQuestion(tool, expected, actual) {
			continue
		}
		question := fmt.Sprintf("Tool %s returned something unexpected, should I rework the prompt around it?", tool)
		// Structured, un-escaped, one concept per line so both the
		// Heartbeat tab and the chat formatter can render it cleanly.
		// oneLine collapses embedded whitespace so each value stays on a
		// single line, the chat formatter splits on "\n" to label parts.
		rationale := fmt.Sprintf("expected: %s\nactual: %s",
			clipShort(oneLine(expected), 240), clipShort(oneLine(actual), 240))
		// Dedup by TOOL, not by prediction id: the question text is keyed on
		// the tool name, so a per-prediction tag would let a second surprise
		// for the same tool collide on the question-text index instead of
		// merging. The prediction id stays in SourceIDs for provenance.
		tag := "high_surprise:tool:" + tool
		if c.insertQuestionTagged(ctx, question, rationale, "high_surprise", tag, []string{id}, 7) {
			n++
		}
	}
	return n, rows.Err()
}

// shouldSuppressHighSurpriseQuestion filters out prediction misses that
// aren't actually worth surfacing as a "should I rework the prompt?"
// card. Two common false positives:
//
//  1. Empty-collection returns. `{"count":0,"items":[]}` /
//     `[]` / `{}` mean "the tool worked fine, there was just nothing
//     to return." Not a prompt-rework signal - the boss doesn't need
//     a heartbeat card for an empty list.
//
//  2. Expected == actual after normalisation. The prediction matched
//     reality; the surprise scorer triggered on whitespace / case /
//     JSON-key ordering noise rather than semantic divergence.
//
// Returns true when the question SHOULD be suppressed (don't create
// a curiosity row). The caller continues to the next surprise row.
func shouldSuppressHighSurpriseQuestion(tool, expected, actual string) bool {
	if strings.EqualFold(strings.TrimSpace(tool), "notify") {
		return true
	}
	a := strings.TrimSpace(actual)
	if a == "" || a == "{}" || a == "[]" || a == "null" {
		return true
	}
	// Empty-collection patterns the agent's tools commonly return:
	// {"count":0,"items":[]}, {"items":[]}, {"results":[]}, etc.
	// Strip whitespace and check for the canonical empty shapes.
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			return -1
		}
		return r
	}, a)
	emptyShapes := []string{
		`{"count":0,"items":[]}`,
		`{"items":[]}`,
		`{"results":[]}`,
		`{"rows":[]}`,
		`{"entries":[]}`,
		`{"data":[]}`,
		`{"count":0}`,
	}
	for _, shape := range emptyShapes {
		if compact == shape {
			return true
		}
	}
	// Whitespace-collapsed equality with expected → no real surprise.
	e := strings.TrimSpace(expected)
	if e != "" && oneLine(e) == oneLine(a) {
		return true
	}
	return false
}

// insertQuestion tries to write a row to mem_curiosity_questions. Routes
// through UpsertQuestion so duplicate topics merge into the existing
// open draft (occurrences_count++, evidence_log gets a new entry,
// importance escalates if needed) instead of generating fresh rows.
//
// The synthesized source_tag is "<kind>:<first source id>" - stable
// enough that re-firing the same detector on the same memory pair
// hits the same row. Detectors without source IDs fall back to a
// kind-only tag, accepting that they'll all merge under one row if
// the question text is genuinely the same.
func (c *CuriosityScan) insertQuestion(ctx context.Context, question, rationale, kind string, sourceIDs []string, importance int) bool {
	tag := kind
	if len(sourceIDs) > 0 {
		tag = kind + ":" + sourceIDs[0]
	}
	return c.insertQuestionTagged(ctx, question, rationale, kind, tag, sourceIDs, importance)
}

// insertQuestionTagged is insertQuestion with an explicit dedup tag, for
// detectors whose question text is coarser than their source ids (e.g.
// high-surprise, where the text is per-tool but the prediction id is per-call).
// The caller controls the merge granularity via tag while SourceIDs still
// carries the precise provenance.
func (c *CuriosityScan) insertQuestionTagged(ctx context.Context, question, rationale, kind, tag string, sourceIDs []string, importance int) bool {
	_, isNew, _, _ := UpsertQuestion(ctx, c.pool, slog.Default(), QuestionDraft{
		Question:   question,
		Rationale:  rationale,
		SourceKind: kind,
		SourceTag:  tag,
		SourceIDs:  sourceIDs,
		Importance: importance,
		Sample:     rationale,
	})
	return isNew
}

// ListOpen returns the open questions, newest first. Used by the heartbeat
// checklist to surface the top-K as findings.
func (c *CuriosityScan) ListOpen(ctx context.Context, limit int) ([]CuriosityQuestion, error) {
	if c == nil || c.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id::text, question, rationale, source_kind, COALESCE(source_tag, ''), importance, created_at
		  FROM mem_curiosity_questions
		 WHERE status = 'open'
		 ORDER BY importance DESC, created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CuriosityQuestion
	for rows.Next() {
		var q CuriosityQuestion
		if err := rows.Scan(&q.ID, &q.Question, &q.Rationale, &q.SourceKind, &q.SourceTag, &q.Importance, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// CuriosityQuestion is the wire shape returned by ListOpen.
type CuriosityQuestion struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	Rationale  string `json:"rationale"`
	SourceKind string `json:"source_kind"`
	SourceTag  string `json:"source_tag"`
	Importance int    `json:"importance"`
	CreatedAt  any    `json:"created_at"`
}

// CuriosityChecklist returns a Checklist function that runs the gap scan and
// converts the top-K open questions into Findings. Compose with
// DefaultChecklist via ComposeChecklists when wiring the heartbeat.
func CuriosityChecklist(pool *pgxpool.Pool) Checklist {
	scanner := NewCuriosityScan(pool)
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		_, _ = scanner.Run(ctx)
		open, err := scanner.ListOpen(ctx, 5)
		if err != nil {
			return nil, err
		}
		out := make([]Finding, 0, len(open))
		for _, q := range open {
			out = append(out, Finding{
				Kind:   "curiosity",
				Title:  q.Question,
				Detail: q.Rationale,
				// SourceKind ("high_surprise" | "contradiction" |
				// "uncovered_mention") lets the chat formatter explain
				// precisely why this question surfaced.
				Source: q.SourceKind,
				// Carry the question's own tag into mem_heartbeat_findings.
				// Without this, every heartbeat wrapped the same open
				// question in an untagged finding, defeating DB dedupe.
				SourceTag: q.SourceTag,
				// CuriosityID lets the chat surface offer "Approve & fix"
				// - it round-trips to /api/curiosity/questions/:id/decide.
				CuriosityID: q.ID,
			})
		}
		return out, nil
	}
}

// ComposeChecklists merges multiple checklist functions into one. Each runs
// in order; findings concatenate. Used to chain DefaultChecklist with
// CuriosityChecklist without giving up either's deterministic behaviour.
func ComposeChecklists(parts ...Checklist) Checklist {
	return func(ctx context.Context, h *Heartbeat) ([]Finding, error) {
		var all []Finding
		for _, p := range parts {
			if p == nil {
				continue
			}
			findings, err := p(ctx, h)
			if err != nil {
				return all, err
			}
			all = append(all, findings...)
		}
		return all, nil
	}
}

func shortQuestion(title, content string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = strings.TrimSpace(content)
	}
	return clipShort(t, 100)
}

func lowConfidenceQuestionSubject(title, content, project string) (string, bool) {
	if strings.TrimSpace(project) == "_self" {
		return "", false
	}

	t := strings.TrimSpace(title)
	c := strings.TrimSpace(content)
	if isGenericProfileLabel(t) && c != "" {
		t = c
	}
	if t == "" {
		t = c
	}
	t = clipShort(t, 100)
	return t, t != ""
}

func isGenericProfileLabel(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "name", "role", "active projects", "communication style", "working hours", "key relationships", "anything else":
		return true
	default:
		return false
	}
}

func clipShort(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// oneLine collapses every run of whitespace (including newlines) into a
// single space. Used on prediction expected/actual values so a rationale
// stays one-concept-per-line - the chat formatter splits the rationale on
// "\n" to label its parts, so embedded newlines would corrupt that.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// uuidArray converts a Go string slice to a pgx-friendly []string. pgx will
// happily marshal []string into a UUID[] when the strings are valid uuids.
func uuidArray(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		s := strings.TrimSpace(id)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
