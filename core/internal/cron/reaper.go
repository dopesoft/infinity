package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
)

// interruptedNarrative is the first-person result for a cron run whose process
// vanished before it could finalize — most often because the nightly
// self-improve loop pushed code, Railway redeployed the core service, and the
// SIGTERM killed the very container running the job. The honest outcome label
// (stopped early / done / needs you) is derived separately from the recorded
// signal; this is just the "why there's no live narrative" line.
const interruptedNarrative = "This run was interrupted before it could wrap up — the box restarted mid-run (most likely a deploy from my own push). " +
	"I recovered the result from what was recorded; I'll confirm the rest on the next pass."

// ReconcileReaped gives a CLEAR boss-facing outcome to every cron run that was
// orphaned — its container died (a self-push redeploy SIGTERM, a crash, an OOM)
// or its finalize block never ran — instead of leaving it as a silent stuck
// 'running' spinner or a bare 'error' with no story. This is the safety net
// behind makeFireFn's fresh-context finalize: when even that 30s window never
// runs because the process is already gone, the next boot / reaper tick
// reconciles the row here so the kanban card and the inbox still tell the truth.
//
// It is IDEMPOTENT and ORDER-INDEPENDENT: it targets cron runs that lack a
// stamped meta.outcome (the live finalize ALWAYS stamps one), regardless of
// whether a generic sweep (runs.RecoverStranded / runs.ReapTimedOut) has already
// flipped the row to 'error'. So it can run before OR after those sweeps and
// still add the outcome + inbox card + cron-row update they omit. The meta-guard
// makes a second pass a no-op.
//
// minAge excludes recently-started runs so a healthy in-flight run is never
// clobbered: the live ticker passes the 45-min reaper budget (longer than the
// 30-min job timeout); the boot sweep passes a small floor (any orphan from
// before the restart is far older than that, but a cron that fired in the first
// seconds after boot is not). Returns the number of runs reconciled.
func (s *Scheduler) ReconcileReaped(ctx context.Context, minAge time.Duration) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id::text, COALESCE(r.target_id,''), COALESCE(c.name,''),
		       COALESCE(c.schedule,''), COALESCE(c.job_kind,''),
		       COALESCE(r.meta,'{}'::jsonb), r.started_at
		  FROM mem_runs r
		  LEFT JOIN mem_crons c ON c.id::text = r.target_id
		 WHERE r.kind = 'cron'
		   AND (r.meta->>'outcome') IS NULL
		   AND r.started_at < NOW() - ($1 * interval '1 second')
		 ORDER BY r.started_at ASC
	`, minAge.Seconds())
	if err != nil {
		return 0, fmt.Errorf("reconcile reaped: query: %w", err)
	}
	type row struct {
		id, cronID, name, schedule, jobKind string
		meta                                map[string]any
		started                             time.Time
	}
	var pending []row
	for rows.Next() {
		var rr row
		var metaRaw []byte
		if err := rows.Scan(&rr.id, &rr.cronID, &rr.name, &rr.schedule, &rr.jobKind, &metaRaw, &rr.started); err != nil {
			continue
		}
		_ = json.Unmarshal(metaRaw, &rr.meta)
		pending = append(pending, rr)
	}
	rows.Close()

	reconciled := 0
	for _, rr := range pending {
		if rr.cronID == "" || rr.name == "" {
			continue // can't address a cron we can't name; the generic sweep still closed the row
		}
		j := Job{
			ID:           rr.cronID,
			Name:         rr.name,
			Schedule:     rr.schedule,
			JobKind:      JobKind(rr.jobKind),
			RunSessionID: strFromMeta(rr.meta, "session_id"),
		}
		// Classify from the REAL recorded signal (turns / plan completeness /
		// http failures) on this FRESH ctx, not a synthetic error: an interrupted
		// run that had actually finished its plan still reads did_work; one cut
		// off mid-plan reads stopped_early; one that hit a 401 reads failed.
		summary := RunSummary{Summary: interruptedNarrative, Meta: rr.meta}
		outcome, summary, execErr := s.finalizeOutcome(ctx, j, summary, nil)

		// Enrich the row in place — works whether it's still 'running' or was
		// already swept to 'error'. Status mirrors FinishByID: 'error' only for a
		// genuine failure; an interrupted-but-not-errored run closes 'ok' with its
		// honest stopped_early / did_work outcome label. The meta.outcome guard in
		// the WHERE makes this atomic + idempotent (a racing pass affects 0 rows).
		status := "ok"
		errStr := ""
		humanJSON := "{}"
		if outcome == OutcomeFailed {
			status = "error"
			if execErr != nil {
				errStr = execErr.Error()
			} else {
				errStr = "this run was interrupted before it could finish"
			}
			if b, mErr := json.Marshal(errs.HumanizeString(errStr)); mErr == nil {
				humanJSON = string(b)
			}
		}
		patch, _ := json.Marshal(map[string]any{"outcome": string(outcome)})
		ct, uerr := s.pool.Exec(ctx, `
			UPDATE mem_runs
			   SET status = $2,
			       ended_at = COALESCE(ended_at, NOW()),
			       duration_ms = COALESCE(duration_ms,
			           LEAST(2147483647, GREATEST(0,
			               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
			       error = CASE WHEN $3 = '' THEN error ELSE $3 END,
			       result_summary = $4,
			       human_error = CASE WHEN human_error IS NULL OR human_error = '{}'::jsonb THEN $5::jsonb ELSE human_error END,
			       meta = COALESCE(meta,'{}'::jsonb) || $6::jsonb
			 WHERE id = $1::uuid
			   AND (meta->>'outcome') IS NULL
		`, rr.id, status, errStr, summary.Summary, humanJSON, string(patch))
		if uerr != nil || ct.RowsAffected() == 0 {
			continue // query error, or another pass already reconciled it
		}
		// Post the rolling inbox card + update the cron's last-run bookkeeping
		// through the SAME writers the live fire path uses, so a redeploy-killed
		// run looks identical to one that finished cleanly. surfaceRunOutcome
		// skips connector polls itself (j.JobKind is set above).
		s.surfaceRunOutcome(ctx, j, summary, execErr, outcome)
		s.recordCronRun(ctx, j, execErr, time.Now().UTC().Sub(rr.started).Milliseconds())
		reconciled++
	}
	return reconciled, nil
}
