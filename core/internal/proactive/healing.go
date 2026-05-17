package proactive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Healing checklist - the "Jarvis noticed something broke" detectors.
//
// Two scanners that fire every heartbeat tick:
//
//  1. Failed crons. mem_crons rows whose last_run_status starts with
//     "error" mean a scheduled job blew up since the last successful
//     fire. Surface each one as a curiosity question with
//     source_kind='cron_failure' so it shows up in /lab's Fix this tab
//     with an Approve-and-fix path that hands the failure to a Live
//     session.
//
//  2. Repeated tool failures. mem_observations rows with
//     hook_name='PostToolUseFailure' grouped by tool name. When the
//     same tool fails 3 or more times in the last 24 hours, that's
//     load-bearing - it's eating turns the boss expected to work.
//     Surface one curiosity question per offending tool with
//     source_kind='repeated_tool_error'.
//
// Both detectors are deterministic SQL; no LLM. Dedup is handled at the
// schema level by mem_curiosity_questions's unique index on
// (question) WHERE status='open' - re-running the scan on the next
// tick is a no-op if the question text is identical and still open.

// Cron-failure detection threshold. last_run_status starts with this
// prefix when scheduler.RunOnce or the regular tick records an error.
const cronErrorPrefix = "error"

// Repeated-tool-error threshold. A tool has to fail at least this many
// times in the look-back window before it earns a Fix-this proposal.
const repeatedErrorThreshold = 3
const repeatedErrorWindow = "24 hours"

// HealingChecklist returns a Checklist function that runs both
// scanners and emits Findings for any newly-detected problem. Compose
// with DefaultChecklist + CuriosityChecklist via ComposeChecklists.
func HealingChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		var findings []Finding
		findings = append(findings, scanCronFailures(ctx, pool)...)
		findings = append(findings, scanRepeatedToolErrors(ctx, pool)...)
		return findings, nil
	}
}

func scanCronFailures(ctx context.Context, pool *pgxpool.Pool) []Finding {
	rows, err := pool.Query(ctx, `
		SELECT id::text,
		       name,
		       COALESCE(last_run_status,''),
		       last_run_at
		  FROM mem_crons
		 WHERE last_run_status LIKE $1
		   AND COALESCE(enabled, TRUE) = TRUE
		 ORDER BY last_run_at DESC NULLS LAST
		 LIMIT 20
	`, cronErrorPrefix+"%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var (
			id, name, status string
			lastRun          *time.Time
		)
		if err := rows.Scan(&id, &name, &status, &lastRun); err != nil {
			continue
		}
		question := fmt.Sprintf("Cron job %q is failing. Fix the routing?", name)
		rationale := truncate(status, 600)
		if lastRun != nil {
			rationale = fmt.Sprintf("Last fired %s. %s",
				lastRun.UTC().Format(time.RFC3339), rationale)
		}
		inserted := insertHealingQuestion(ctx, pool,
			question, rationale, "cron_failure", []string{id}, 9)
		if !inserted {
			// Already-open question for this cron. Skip the Finding so
			// heartbeat noise stays low; the previous tick already
			// surfaced this and nothing has changed.
			continue
		}
		out = append(out, Finding{
			Kind:   "self_heal",
			Source: "cron_failure",
			Title:  question,
			Detail: rationale,
			// Per-cron source_tag so the next tick's finding for the
			// same cron supersedes this one even if the error message
			// changes (e.g. routing fix lands and the same cron fails
			// for a different reason).
			SourceTag: "cron_failure:" + id,
		})
	}
	return out
}

func scanRepeatedToolErrors(ctx context.Context, pool *pgxpool.Pool) []Finding {
	// Group PostToolUseFailure observations by tool name (extracted from
	// the JSON payload). A tool that fails THRESHOLD+ times in WINDOW
	// gets one curiosity question; the sample error is the most recent
	// occurrence so the rationale carries something actionable.
	rows, err := pool.Query(ctx, `
		WITH failures AS (
			SELECT
				payload->>'name' AS tool_name,
				payload->>'output' AS sample_output,
				created_at
			  FROM mem_observations
			 WHERE hook_name = 'PostToolUseFailure'
			   AND created_at > NOW() - INTERVAL '`+repeatedErrorWindow+`'
			   AND COALESCE(payload->>'name','') <> ''
		),
		grouped AS (
			SELECT
				tool_name,
				COUNT(*) AS hits,
				MAX(created_at) AS last_seen,
				(ARRAY_AGG(sample_output ORDER BY created_at DESC))[1] AS sample
			  FROM failures
			 GROUP BY tool_name
		)
		SELECT tool_name, hits, last_seen, COALESCE(sample,'')
		  FROM grouped
		 WHERE hits >= $1
		 ORDER BY hits DESC, last_seen DESC
		 LIMIT 10
	`, repeatedErrorThreshold)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var (
			tool, sample string
			hits         int
			lastSeen     time.Time
		)
		if err := rows.Scan(&tool, &hits, &lastSeen, &sample); err != nil {
			continue
		}
		question := fmt.Sprintf("Tool %q has failed %d times in the last 24h. Fix it?", tool, hits)
		rationale := fmt.Sprintf("Most recent failure %s\n\n%s",
			lastSeen.UTC().Format(time.RFC3339), truncate(sample, 600))
		inserted := insertHealingQuestion(ctx, pool,
			question, rationale, "repeated_tool_error", nil, 8)
		if !inserted {
			continue
		}
		out = append(out, Finding{
			Kind:   "self_heal",
			Source: "repeated_tool_error",
			Title:  question,
			Detail: rationale,
			// Per-tool source_tag so the count-varying title
			// ("3 times" -> "6 times") doesn't stack rows.
			SourceTag: "repeated_tool_error:" + tool,
		})
	}
	return out
}

// insertHealingQuestion writes (or no-ops on conflict) a curiosity
// question with the given source_kind. Dedupe rides on the unique
// index on (question) WHERE status='open' so re-runs of the scan are
// idempotent across heartbeat ticks. Returns true when a NEW row
// landed so the caller can decide whether to emit a Finding.
func insertHealingQuestion(
	ctx context.Context,
	pool *pgxpool.Pool,
	question, rationale, sourceKind string,
	sourceIDs []string,
	importance int,
) bool {
	if pool == nil {
		return false
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO mem_curiosity_questions
		  (question, rationale, source_kind, source_ids, importance, status)
		VALUES ($1, $2, $3, $4::uuid[], $5, 'open')
		ON CONFLICT DO NOTHING
	`, question, rationale, sourceKind, uuidArray(sourceIDs), importance)
	if err != nil {
		return false
	}
	return tag.RowsAffected() > 0
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
