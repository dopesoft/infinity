package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReflectionChainsProvider implements agent.MemoryProvider, folding cross-
// session meta-lessons from mem_reflection_chains into every turn.
//
// Reflections critique one session; chains cluster repeated lessons across
// sessions. Without this provider, chains exist in the database but never
// reach the model - the agent reflects, persists, then forgets.
//
// To avoid repeating the same chain forever, the provider round-robins among
// the top-confidence chains: each call picks the eligible chain with the
// oldest last_referenced_at (or NULL) and bumps the stamp. Migration 053
// adds the column.
type ReflectionChainsProvider struct {
	pool *pgxpool.Pool
}

func NewReflectionChainsProvider(pool *pgxpool.Pool) *ReflectionChainsProvider {
	return &ReflectionChainsProvider{pool: pool}
}

func (p *ReflectionChainsProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	const sql = `
		WITH eligible AS (
			SELECT id, topic, lesson, occurrences, confidence, last_referenced_at
			  FROM mem_reflection_chains
			 WHERE confidence > 0.7
			   AND last_seen_at > NOW() - INTERVAL '30 days'
			 ORDER BY last_referenced_at NULLS FIRST, confidence DESC, occurrences DESC
			 LIMIT 3
		),
		updated AS (
			UPDATE mem_reflection_chains
			   SET last_referenced_at = NOW()
			 WHERE id IN (SELECT id FROM eligible)
			 RETURNING id
		)
		SELECT topic, lesson, occurrences, confidence FROM eligible
	`
	rows, err := p.pool.Query(ctx, sql)
	if err != nil {
		return "", nil
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var (
			topic, lesson string
			occurrences   int
			confidence    float64
		)
		if err := rows.Scan(&topic, &lesson, &occurrences, &confidence); err != nil {
			continue
		}
		if count == 0 {
			b.WriteString("<cross_session_lessons>\n")
			b.WriteString("Lessons distilled from prior sessions (mem_reflection_chains). Apply when the situation matches.\n")
		}
		fmt.Fprintf(&b, "- %s (seen %d×, confidence %.2f): %s\n",
			topic, occurrences, confidence, trimLessonLine(lesson, 280))
		count++
	}
	if count == 0 {
		return "", nil
	}
	b.WriteString("</cross_session_lessons>")
	return b.String(), nil
}

func trimLessonLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
