package proactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CuriosityScan looks for gaps in the agent's knowledge graph and produces
// questions to ask the user. This is the active-learning loop CoALA and
// Generative Agents (Park et al.) describe: the agent doesn't only answer
// questions, it FINDS gaps and surfaces them.
//
// Four signal types:
//   1. low_confidence  — semantic memories whose strength has decayed under
//                        0.35 but haven't been forgotten yet.
//   2. contradiction   — unresolved 'contradicts' edges where both memories
//                        are still active.
//   3. uncovered_mention — graph nodes referenced repeatedly with no memory
//                          providing context.
//   4. high_surprise   — predictions where surprise_score >= 0.8.
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
		SELECT id::text, COALESCE(title, ''), COALESCE(content, '')
		  FROM mem_memories
		 WHERE tier = 'semantic'
		   AND status = 'active'
		   AND strength < 0.35
		   AND created_at < NOW() - INTERVAL '24 hours'
		 ORDER BY strength ASC, updated_at ASC
		 LIMIT 5
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, title, content string
		if err := rows.Scan(&id, &title, &content); err != nil {
			continue
		}
		question := fmt.Sprintf("Is this still true: %s?", shortQuestion(title, content))
		rationale := "Semantic memory has decayed below confidence threshold — ask the boss to confirm or retire it."
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
		question := fmt.Sprintf("Two memories disagree — which is right: %q or %q?",
			clipShort(srcTitle, 80), clipShort(tgtTitle, 80))
		rationale := "Both memories are active but a 'contradicts' edge links them — need the boss to resolve."
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
		question := fmt.Sprintf("The boss has mentioned %s %q multiple times — what's important about it?", kind, name)
		rationale := "Repeated graph mentions with no derived memory — gap worth filling."
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
		question := fmt.Sprintf("Tool %s returned something unexpected — should I rework the prompt around it?", tool)
		rationale := fmt.Sprintf("expected=%q actual=%q (surprise≥0.8)", clipShort(expected, 80), clipShort(actual, 80))
		if c.insertQuestion(ctx, question, rationale, "high_surprise", []string{id}, 7) {
			n++
		}
	}
	return n, rows.Err()
}

// insertQuestion tries to write a row to mem_curiosity_questions. The unique
// index on (question) WHERE status='open' makes this idempotent — duplicate
// inserts no-op. Returns true when a new row was written.
func (c *CuriosityScan) insertQuestion(ctx context.Context, question, rationale, kind string, sourceIDs []string, importance int) bool {
	if strings.TrimSpace(question) == "" {
		return false
	}
	tag, err := c.pool.Exec(ctx, `
		INSERT INTO mem_curiosity_questions
		  (question, rationale, source_kind, source_ids, importance, status)
		VALUES ($1, $2, $3, $4::uuid[], $5, 'open')
		ON CONFLICT DO NOTHING
	`, question, rationale, kind, uuidArray(sourceIDs), importance)
	if err != nil {
		fmt.Printf("[curiosity] insert: %v\n", err)
		return false
	}
	return tag.RowsAffected() > 0
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
		SELECT id::text, question, rationale, source_kind, importance, created_at
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
		if err := rows.Scan(&q.ID, &q.Question, &q.Rationale, &q.SourceKind, &q.Importance, &q.CreatedAt); err != nil {
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

func clipShort(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
