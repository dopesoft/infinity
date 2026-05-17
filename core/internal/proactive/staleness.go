// Package proactive - staleness.go: the aggressive tiered-TTL sweep
// for curiosity questions and heartbeat findings.
//
// The bug this closes: even with source_tag dedup and bulk-dismiss the
// dashboard fills up because nothing GARBAGE COLLECTS rows that the
// boss neither dismissed nor acted on. They just sit there forever.
//
// The fix: every heartbeat tick, FIRST thing, a single SQL UPDATE
// flips stale 'open' rows to 'dismissed' with a tiered TTL keyed on
// importance. Low-importance / unranked questions evaporate in hours;
// medium die in a day; high last 3 days; only critical survives a
// week. A "touched" question (boss opened or interacted with it,
// reflected in last_seen_at) gets a 2x TTL multiplier so engagement
// extends life and disinterest cleans up automatically.
//
// One SQL round-trip. No LLM. Idempotent. Called as the first step of
// every Heartbeat.Tick so the dashboard self-cleans in near-real-time
// (tick interval is minutes, not days).

package proactive

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterAsConsolidateHook lets the caller add SweepStaleness as a
// final stage of memory.ConsolidateNightly. Defense in depth: heartbeat
// runs every few minutes, but if heartbeat is paused the daily
// consolidation still keeps the dashboard tidy. Call once at boot.
// The pool capture closes over the supplied logger so log output goes
// to the same destination as the rest of the consolidate report.
func RegisterAsConsolidateHook(register func(func(ctx context.Context, pool *pgxpool.Pool)), logger *slog.Logger) {
	if register == nil {
		return
	}
	register(func(ctx context.Context, pool *pgxpool.Pool) {
		SweepStaleness(ctx, pool, logger)
	})
}

// SweepStaleness flips stale 'open' rows in both mem_curiosity_questions
// and mem_heartbeat_findings to 'dismissed' according to the tiered
// TTL policy. Returns (questionsSwept, findingsSwept). Best-effort: a
// failed sweep logs and returns zeros; the next tick retries.
//
// TTL policy (multiplied by 2 when last_seen_at IS NOT NULL):
//
//	importance NULL    -> 12h    (stale_unranked)
//	importance 1-3     ->  6h    (stale_low_importance)
//	importance 4-6     -> 24h    (stale_medium_importance)
//	importance 7-8     ->  3d    (stale_high_importance)
//	importance 9-10    ->  7d    (stale_age_cap)
func SweepStaleness(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (int, int) {
	if pool == nil {
		return 0, 0
	}
	qSwept := sweepStaleQuestions(ctx, pool, logger)
	fSwept := sweepStaleFindings(ctx, pool, logger)
	if logger != nil && (qSwept+fSwept) > 0 {
		// stdout via the boot info logger - this is a routine maintenance
		// signal, NOT an error. Going to stderr (default for stdlib log)
		// would paint these as severity:error in Railway.
		logger.Info("staleness sweep",
			"questions_auto_dismissed", qSwept,
			"findings_auto_dismissed", fSwept)
	}
	return qSwept, fSwept
}

func sweepStaleQuestions(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) int {
	tag, err := pool.Exec(ctx, `
		UPDATE mem_curiosity_questions
		   SET status            = 'dismissed',
		       resolved_at       = NOW(),
		       auto_dismissed_at = NOW(),
		       resolved_reason   = CASE
		           WHEN importance IS NULL THEN 'stale_unranked'
		           WHEN importance < 4     THEN 'stale_low_importance'
		           WHEN importance < 7     THEN 'stale_medium_importance'
		           WHEN importance < 9     THEN 'stale_high_importance'
		           ELSE                        'stale_age_cap'
		       END
		 WHERE status = 'open'
		   AND created_at < NOW() - (CASE
		           WHEN importance IS NULL THEN INTERVAL '12 hours'
		           WHEN importance < 4     THEN INTERVAL '6 hours'
		           WHEN importance < 7     THEN INTERVAL '24 hours'
		           WHEN importance < 9     THEN INTERVAL '3 days'
		           ELSE                        INTERVAL '7 days'
		       END * (CASE WHEN last_seen_at IS NOT NULL THEN 2 ELSE 1 END))
	`)
	if err != nil {
		if logger != nil {
			logger.Warn("staleness: questions sweep", "err", err)
		}
		return 0
	}
	return int(tag.RowsAffected())
}

func sweepStaleFindings(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) int {
	tag, err := pool.Exec(ctx, `
		UPDATE mem_heartbeat_findings
		   SET status            = 'dismissed',
		       resolved_at       = NOW(),
		       auto_dismissed_at = NOW()
		 WHERE status = 'open'
		   AND created_at < NOW() - (CASE
		           WHEN importance IS NULL THEN INTERVAL '12 hours'
		           WHEN importance < 4     THEN INTERVAL '6 hours'
		           WHEN importance < 7     THEN INTERVAL '24 hours'
		           WHEN importance < 9     THEN INTERVAL '3 days'
		           ELSE                        INTERVAL '7 days'
		       END * (CASE WHEN last_seen_at IS NOT NULL THEN 2 ELSE 1 END))
	`)
	if err != nil {
		if logger != nil {
			logger.Warn("staleness: findings sweep", "err", err)
		}
		return 0
	}
	return int(tag.RowsAffected())
}
