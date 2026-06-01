package proactive

import (
	"context"
	"log/slog"
	"strings"

	"github.com/dopesoft/infinity/core/internal/proposals"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RaiseSkillFragmentation is the nightly backstop that flags genuine skill
// duplicates (catalog hygiene), wired as a memory.RegisterConsolidateHook.
//
// It is deliberately thin: proposals.DetectDuplicateClusters does the
// (deterministic, name-based) detection, and this raises the result through the
// SAME cooldown-aware UpsertQuestion path every other detector uses — so a
// dismissal sets cooldown_until and the question does NOT come back next run.
// The previous version raw-INSERTed into mem_curiosity_questions, bypassing the
// cooldown machinery, which is why a dismissed card reappeared an hour later.
//
// No clusters → resolve any open row so the card self-clears. Pool-only;
// nil-safe.
func RaiseSkillFragmentation(ctx context.Context, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	lines := proposals.DetectDuplicateClusters(ctx, pool)
	if len(lines) == 0 {
		// Self-heal: clear an open fragmentation question once the catalog is clean.
		_, _ = pool.Exec(ctx, `
			UPDATE mem_curiosity_questions
			SET status='resolved', resolved_at=NOW()
			WHERE source_tag='skill_fragmentation' AND status='open'
		`)
		return
	}

	question := "Duplicate skills detected — collapse each domain into one flexible recipe"
	rationale := "These active skills share a name root and likely do the same job; keep ONE and archive the rest so the capability stays single and improvable:\n" +
		strings.Join(lines, "\n")

	UpsertQuestion(ctx, pool, slog.Default(), QuestionDraft{
		Question:   question,
		Rationale:  rationale,
		SourceKind: "skill_fragmentation",
		SourceTag:  "skill_fragmentation",
		Importance: 6,
		Sample:     strings.Join(lines, "; "),
	})
}
