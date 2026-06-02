package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LessonsProvider implements agent.MemoryProvider, injecting the agent's
// highest-conviction behavioral lessons (mem_lessons) into every turn.
//
// Before this, mem_lessons was WRITE-ONLY: reflection promoted lessons into it,
// but no provider read them, so they never reached the model (the audit's
// "dead table" finding). reflection_chains injects CLUSTERED cross-session
// lessons; this complements it by surfacing the individual, battle-tested
// behavioral rules — the strong ones ("on a 401, stop and escalate, don't
// retry", "never declare a fix done until verified end-to-end", "when the boss
// forbids a tool it's a hard constraint").
//
// To avoid repeating the same rules forever it round-robins on
// last_referenced_at (migration 111). Only confidence >= 0.85 lessons qualify,
// so freshly-minted weak lessons stay out of the prompt until they prove out.
type LessonsProvider struct {
	pool *pgxpool.Pool
}

func NewLessonsProvider(pool *pgxpool.Pool) *LessonsProvider {
	return &LessonsProvider{pool: pool}
}

func (p *LessonsProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	const sql = `
		WITH eligible AS (
			SELECT id, lesson_text, confidence, COALESCE(times_reinforced, 0) AS reinforced
			  FROM mem_lessons
			 WHERE confidence >= 0.85
			 ORDER BY last_referenced_at NULLS FIRST, reinforced DESC, confidence DESC
			 LIMIT 3
		),
		updated AS (
			UPDATE mem_lessons SET last_referenced_at = NOW()
			 WHERE id IN (SELECT id FROM eligible)
			 RETURNING id
		)
		SELECT lesson_text, confidence, reinforced FROM eligible
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
			text       string
			confidence float64
			reinforced int
		)
		if err := rows.Scan(&text, &confidence, &reinforced); err != nil {
			continue
		}
		if count == 0 {
			b.WriteString("<proven_lessons>\n")
			b.WriteString("Hard-won behavioral rules from past sessions (mem_lessons). Follow them.\n")
		}
		if reinforced > 0 {
			fmt.Fprintf(&b, "- (reinforced %d×) %s\n", reinforced, trimLessonLine(text, 280))
		} else {
			fmt.Fprintf(&b, "- %s\n", trimLessonLine(text, 280))
		}
		count++
	}
	if count == 0 {
		return "", nil
	}
	b.WriteString("</proven_lessons>")
	return b.String(), nil
}
