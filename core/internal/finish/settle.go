package finish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Settling a coding job from its OWN transcript, before anything else acts on
// the row.
//
// THE FAILURE. 2026-08-29: a 47-minute Claude Code build finished
// successfully, wrote a full report, and Infinity told the boss it had failed.
// Two separate defects, both fixed here:
//
//  1. Nothing re-read a finished transcript. The poll loop reads it while
//     someone is watching; when the window closed, the report sat on the Mac
//     and no code ever opened it again. The run stayed "still working", the
//     plan step stayed `blocked`, and the work read as lost.
//  2. A dead `running` row blocked recovery. Coding kinds are exempt from the
//     blanket reaper (correctly — a wall clock cannot tell a 50-minute build
//     from a dead one), so a row whose process was gone kept claiming to be
//     live, which held the one-job-per-conversation guard shut AND held this
//     poller's own "never while newer coding work is live" check shut.
//
// The answer to both is one read-only probe of the job's own files, asking
// three questions: are they there, is the process alive, did Claude write its
// terminal result. Deterministic Go, no cognition (Rule #1b) — the judgment
// that follows ("tell him, settle the step") is the model's, as always.

// Verdict is what a job's own files say about it. Mirrors
// tools.ClaudeJobVerdict; declared here so this package keeps its no-bridge,
// no-network testability.
type Verdict struct {
	Found   bool
	Alive   bool
	Done    bool
	IsError bool
	Report  string
	Files   []string
	Err     string
}

// Looked reports whether the probe actually ran. An unreachable Mac must never
// read as "the job left nothing behind".
func (v Verdict) Looked() bool { return v.Err == "" }

// Transcript reads a coding job's own files off the bridge that ran it.
type Transcript interface {
	Read(ctx context.Context, sessionID, repo, runID string) Verdict
}

// candidate is one run worth probing.
type candidate struct {
	runID     string
	label     string
	sessionID string
	repo      string
	claudeSes string
	status    string
	reason    string
	startedAt time.Time
}

// settleTriesSQL counts CONSECUTIVE FAILED probes, guarded like the other
// numeric meta reads because Postgres has no TRY_CAST.
const settleTriesSQL = `CASE WHEN meta->>'settle_tries' ~ '^[0-9]+$'
                             THEN (meta->>'settle_tries')::int ELSE 0 END`

// SettleOne probes at most one coding run per tick and settles it from its own
// transcript. Returns true when it changed something.
//
// One per tick, like ContinueOne: each probe is a bridge round trip, and there
// is never a hurry — the row has already stopped moving.
func (p *Poller) SettleOne(ctx context.Context) (bool, error) {
	if p == nil || p.pool == nil || p.transcript == nil {
		return false, nil
	}
	c, err := p.claimSettle(ctx)
	if err != nil || c == nil {
		return false, err
	}
	v := p.transcript.Read(ctx, c.sessionID, c.repo, c.runID)
	if !v.Looked() {
		// Could not look. The try counter already went up at claim time, so a
		// Mac that stays asleep stops being probed rather than being probed
		// forever, and the row is left exactly as it was.
		return false, fmt.Errorf("probe run %s: %s", c.runID, v.Err)
	}
	// The probe worked, so the failure counter starts again from zero: a long
	// job that is simply still going must never exhaust its attempts and
	// become unsettleable.
	p.note(ctx, c.runID, "settle_tries", "0")

	switch {
	case v.Done:
		return true, p.closeCompleted(ctx, *c, v)
	case v.Alive:
		// Still working. Nothing to do, and deliberately nothing said: the row
		// already carries its own progress line.
		return false, nil
	case c.status == "running" && !v.Found:
		// No process and no files. This row cannot be describing live work,
		// and while it claims to be, it blocks every path behind it.
		return true, p.closeDead(ctx, *c)
	}
	return false, nil
}

// claimSettle takes the next probe-worthy run and books the attempt in the
// same statement, so two ticks can never probe the same row and a crash
// between the claim and the probe costs one attempt rather than looping.
func (p *Poller) claimSettle(ctx context.Context) (*candidate, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE mem_runs SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object(
		         'settle_tries',   (`+settleTriesSQL+`) + 1,
		         'settle_last_at', to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
		 WHERE id = (
		   SELECT r.id FROM mem_runs r
		    WHERE r.kind = ANY($1)
		      -- CLAUDE CODE ONLY. This probe reads a DETACHED job's own files
		      -- under /tmp/inf-code on the Mac. A cloud build has none — Jarvis
		      -- writes that code himself inside the agent loop — so probing one
		      -- would come back "no files, no process" and this would close a
		      -- perfectly healthy run as dead. Both Mac paths stamp
		      -- meta.engine; nothing on the cloud does.
		      AND COALESCE(r.meta->>'engine','') = 'claude_code'
		      AND COALESCE(r.meta->>'repo','') <> ''
		      AND r.started_at > NOW() - $2::interval
		      AND (CASE WHEN r.meta->>'settle_tries' ~ '^[0-9]+$'
		                THEN (r.meta->>'settle_tries')::int ELSE 0 END) < $3
		      AND (COALESCE(r.meta->>'settle_last_at','') !~ '^\d{4}-\d{2}-\d{2}T'
		           OR (r.meta->>'settle_last_at')::timestamptz < NOW() - $4::interval)
		      AND (
		            -- Live but quiet: either finished with nobody watching, or
		            -- the row is describing a process that no longer exists.
		            (r.status = 'running' AND (`+lastActivitySQL+`) < NOW() - $5::interval)
		            -- Closed without a verdict: the case that reported a
		            -- successful 47-minute build as a failure.
		         OR (r.status <> 'running'
		             AND COALESCE(r.meta->>'stopped_reason','') <> ''
		             AND r.ended_at IS NOT NULL
		             AND r.ended_at < NOW() - $6::interval)
		          )
		    ORDER BY r.started_at
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED)
		RETURNING id::text,
		          label,
		          COALESCE(meta->>'session_id',''),
		          COALESCE(meta->>'repo',''),
		          COALESCE(meta->>'claude_session_id',''),
		          status,
		          COALESCE(meta->>'stopped_reason',''),
		          started_at
	`, codingKinds, p.lookback.String(), p.maxSettleTries, p.settleEach.String(),
		p.stallAfter.String(), p.settleGrace.String())

	var c candidate
	err := row.Scan(&c.runID, &c.label, &c.sessionID, &c.repo, &c.claudeSes,
		&c.status, &c.reason, &c.startedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// closeCompleted turns a run that actually finished into one that SAYS it
// finished, and then wakes Jarvis with the report so the boss hears it in the
// chat he started it from.
//
// The row is corrected first and the chat second, deliberately: if the replay
// fails, the truth is still on the board rather than depending on a second
// thing also working.
func (p *Poller) closeCompleted(ctx context.Context, c candidate, v Verdict) error {
	status := "ok"
	if v.IsError {
		status = "error"
	}
	summary := strings.TrimSpace(v.Report)
	if summary == "" {
		summary = "It finished, but it wrote no closing summary. " + filesLine(v.Files)
	}
	if _, err := p.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status         = $2,
		       ended_at       = COALESCE(ended_at, NOW()),
		       result_summary = $3,
		       progress       = 1,
		       progress_label = '',
		       meta = (COALESCE(meta,'{}'::jsonb) - 'stopped_reason' - 'stalled_since')
		              || jsonb_build_object(
		                   'finish_outcome', 'completed',
		                   'settled_at', to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
		 WHERE id = $1::uuid`, c.runID, status, clip(summary, 8000)); err != nil {
		return fmt.Errorf("close completed run %s: %w", c.runID, err)
	}
	infoLog.Printf("run %s actually finished (%s) — corrected the row and reporting it into session %s",
		c.runID, status, c.sessionID)

	if !isChatSession(c.sessionID) || p.replayer == nil {
		return nil
	}
	if _, err := p.replayer.Replay(ctx, c.sessionID, buildCompletedBrief(c, v), ""); err != nil {
		p.note(ctx, c.runID, "finish_error", err.Error())
		return fmt.Errorf("report finished run %s into session %s: %w", c.runID, c.sessionID, err)
	}
	p.note(ctx, c.runID, "finish_outcome", "reported")
	return nil
}

// closeDead closes a `running` row whose job is provably gone: no process, no
// files. It closes 'ok' + still_working, matching how every other interruption
// is recorded — the job was never stopped and never failed, we simply have no
// verdict — and that stamp is what makes ContinueOne pick it up next tick.
func (p *Poller) closeDead(ctx context.Context, c candidate) error {
	summary := "I lost track of this one. Its process is gone from the Mac and it left nothing behind, " +
		"so I have no result for it — it was not stopped and it did not fail. Anything it wrote is still " +
		"uncommitted in " + firstNonEmpty(c.repo, "that repo") + "."
	if _, err := p.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status         = 'ok',
		       ended_at       = COALESCE(ended_at, NOW()),
		       duration_ms    = COALESCE(duration_ms,
		           LEAST(2147483647, GREATEST(0, EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
		       progress_label = '',
		       result_summary = COALESCE(NULLIF(result_summary, ''), $2),
		       meta = COALESCE(meta,'{}'::jsonb)
		              || jsonb_build_object('stopped_reason', 'still_working', 'finish_outcome', 'reaped')
		 WHERE id = $1::uuid
		   AND status = 'running'`, c.runID, summary); err != nil {
		return fmt.Errorf("close dead run %s: %w", c.runID, err)
	}
	infoLog.Printf("run %s had no process and no files left — closed it so the work behind it can move", c.runID)
	return nil
}

// filesLine names what the transcript showed it touching, for a job that
// finished without writing its own summary.
func filesLine(files []string) string {
	if len(files) == 0 {
		return "Check git_status in that repo for what it changed."
	}
	return "It touched " + strings.Join(clipList(files, 8), ", ") + "."
}
