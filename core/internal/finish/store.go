package finish

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runRows is every statement the poller runs against mem_runs and
// mem_sessions, behind one seam.
//
// It exists so the tick loop can be driven end to end in a test with a clock
// and no Postgres: the 2026-09-02 shape (one stranded run, a tick a minute for
// fifteen minutes) is pinned by such a test, and the rules it asserts (three
// briefs at most, never into the boss's chat, a real gap between them) are the
// ones that cost 28% of a week's Claude plan when they were not there. The
// live implementation is pgRows; the SQL is the same SQL as before, moved.
type runRows interface {
	// MarkStalled makes a running job that has shown no new activity say so.
	MarkStalled(ctx context.Context, stallAfter time.Duration) (int, error)
	// ClaimContinue atomically takes the next stranded run and books its
	// pass, minting the run's continuation session id on first claim.
	ClaimContinue(ctx context.Context, q claimParams) (*stranded, error)
	// ClaimSettle atomically takes the next probe-worthy run and books the
	// attempt.
	ClaimSettle(ctx context.Context, q settleParams) (*candidate, error)
	// Note records one meta key on the run row.
	Note(ctx context.Context, runID, key, value string) error
	// Spend closes a run's continuation budget so it is never raised again.
	Spend(ctx context.Context, runID string, maxPasses int) error
	// CloseCompleted corrects a run that actually finished.
	CloseCompleted(ctx context.Context, runID, status, summary string) error
	// CloseDead closes a `running` row whose job is provably gone.
	CloseDead(ctx context.Context, runID, summary string) error
	// EnsureContinuationSession creates the session a stranded run is
	// continued in, inheriting the parent chat's project and bridge. It is
	// idempotent, so later passes of the same run land in the same session.
	EnsureContinuationSession(ctx context.Context, sessionID, parentSessionID, name, runID string) error
	// ActivePlanID is the plan the parent chat is currently driving, or "".
	ActivePlanID(ctx context.Context, sessionID string) (string, error)
}

// claimParams are the rules ClaimContinue applies. They are the poller's own
// tunables, passed explicitly so the SQL and the test fake read the same
// numbers.
type claimParams struct {
	settleGrace time.Duration
	lookback    time.Duration
	maxPasses   int
	// backoff is the minimum gap after a run's last brief before it may be
	// claimed again. Without it a run was re-briefed on the very next tick,
	// three full model turns in three minutes (2026-09-02, 05:45-06:00).
	backoff time.Duration
	// requireSettled gates a Claude Code run on SettleOne having read its
	// own files first (see the comment inside claimSQL).
	requireSettled bool
}

// settleParams are the rules ClaimSettle applies.
type settleParams struct {
	lookback    time.Duration
	maxTries    int
	each        time.Duration
	stallAfter  time.Duration
	settleGrace time.Duration
}

// pgRows is runRows over the live database.
type pgRows struct{ pool *pgxpool.Pool }

var errNoPool = errors.New("finish: no database pool")

func (r pgRows) MarkStalled(ctx context.Context, stallAfter time.Duration) (int, error) {
	if r.pool == nil {
		return 0, errNoPool
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET progress_label = 'Claude Code · no new activity for '
		         || FLOOR(EXTRACT(EPOCH FROM (NOW() - (`+lastActivitySQL+`)))/60)::int || 'm'
		         || COALESCE(NULLIF(' · last: ' || (meta->>'currentFile'), ' · last: '), ''),
		       meta = COALESCE(meta,'{}'::jsonb)
		              || jsonb_build_object('stalled_since', to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
		 WHERE kind = ANY($1)
		   AND status = 'running'
		   AND COALESCE(meta->>'stalled_since','') = ''
		   AND (`+lastActivitySQL+`) < NOW() - $2::interval
	`, codingKinds, stallAfter.String())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// claimSQL is the statement ClaimContinue runs. Split out so the rules encoded
// in it can be asserted by a test instead of described in a comment: the
// "only the newest stranded job" clause is what stands between the boss and
// thirty messages he never asked for, and the backoff clause is what stands
// between him and a model turn a minute.
func claimSQL() string {
	return `
		UPDATE mem_runs SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object(
		         'finish_passes',  (` + passesSQL + `) + 1,
		         'finish_last_at', to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		         -- THE CONTINUATION HAPPENS IN ITS OWN SESSION, NEVER THE BOSS'S CHAT.
		         -- Minted here, in the same statement as the claim, so a crash
		         -- between the claim and the replay cannot leave a run with two
		         -- of them, and every later pass of this run reuses the one id
		         -- (the passes share context). On 2026-09-02 these briefs went
		         -- into a live conversation whose context was ~900K tokens, so
		         -- each one was a full Opus turn to answer in under 120 tokens.
		         'finish_session_id', COALESCE(NULLIF(meta->>'finish_session_id',''), uuid_generate_v4()::text))
		 WHERE id = (
		   SELECT r.id FROM mem_runs r
		    WHERE r.kind = ANY($1)
		      AND r.status <> 'running'
		      AND COALESCE(r.meta->>'stopped_reason','') <> ''
		      AND r.ended_at IS NOT NULL
		      AND r.ended_at <  NOW() - $2::interval
		      AND r.ended_at >  NOW() - $3::interval
		      AND (CASE WHEN r.meta->>'finish_passes' ~ '^[0-9]+$'
		                THEN (r.meta->>'finish_passes')::int ELSE 0 END) < $4
		      -- BACK OFF AFTER A BRIEF. A run that was briefed less than one
		      -- settle window ago is not asked again on the next tick: the
		      -- model has had its say and the repo has not had time to move.
		      AND (COALESCE(r.meta->>'finish_last_at','') !~ '^\d{4}-\d{2}-\d{2}T'
		           OR (r.meta->>'finish_last_at')::timestamptz < NOW() - $6::interval)
		      AND COALESCE(r.meta->>'session_id','') <> ''
		      AND COALESCE(r.meta->>'repo','')       <> ''
		      -- READ IT BEFORE YOU RE-RUN IT. When a transcript reader is
		      -- wired, a Claude Code run is only continued after SettleOne has
		      -- actually looked at its own files ($5). Without this gate the
		      -- two claims pick rows by different orderings, and a job whose
		      -- report is sitting unread on the Mac gets relaunched — the
		      -- 2026-08-29 failure, made autonomous.
		      --
		      -- A CLOUD build is exempt, because there is nothing to read:
		      -- Jarvis writes that code himself inside the agent loop, so no
		      -- transcript exists and SettleOne skips it by design. Without
		      -- this clause the gate would never open and cloud work would
		      -- silently stop being continued at all.
		      AND (NOT $5::bool
		           OR COALESCE(r.meta->>'engine','') <> 'claude_code'
		           OR COALESCE(r.meta->>'settle_last_at','') <> '')
		      -- Never while newer coding work is live in the same chat: that
		      -- job may BE the continuation, and two drivers on one repo is
		      -- how the same edit gets made twice.
		      AND NOT EXISTS (
		            SELECT 1 FROM mem_runs live
		             WHERE live.kind = ANY($1)
		               AND live.status = 'running'
		               AND live.meta->>'session_id' = r.meta->>'session_id')
		      -- ONLY THE NEWEST STRANDED JOB IN A CHAT IS STILL A QUESTION.
		      --
		      -- A long build is a chain of small passes, so one conversation
		      -- accumulates dozens of them, and every earlier pass is stranded
		      -- by definition the moment the next one starts. Asking about each
		      -- in turn is asking about work that has already been superseded.
		      --
		      -- The boss walked away for twenty minutes and came back to
		      -- thirty identical messages (2026-09-02): twenty-four stranded
		      -- jobs in one chat, one nudge a minute, Jarvis patiently
		      -- answering "not resuming, it is already committed" to each. The
		      -- per-run pass budget bounded each job and nothing bounded the
		      -- QUEUE, so the loop was correct and unbearable at the same time.
		      --
		      -- Whatever happened after this run is the real state of the
		      -- work, so this one has nothing left to say.
		      AND NOT EXISTS (
		            SELECT 1 FROM mem_runs newer
		             WHERE newer.kind = ANY($1)
		               AND newer.meta->>'session_id' = r.meta->>'session_id'
		               AND newer.ended_at IS NOT NULL
		               AND newer.ended_at > r.ended_at)
		    ORDER BY r.ended_at
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED)
		RETURNING id::text,
		          label,
		          COALESCE(meta->>'session_id',''),
		          COALESCE(meta->>'repo',''),
		          COALESCE(meta->>'claude_session_id',''),
		          COALESCE(meta->>'stopped_reason',''),
		          COALESCE(result_summary,''),
		          COALESCE(meta->>'currentFile',''),
		          (` + passesSQL + `),
		          started_at,
		          ended_at,
		          COALESCE(meta->>'finish_session_id','')
	`
}

func (r pgRows) ClaimContinue(ctx context.Context, q claimParams) (*stranded, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	row := r.pool.QueryRow(ctx, claimSQL(), codingKinds, q.settleGrace.String(), q.lookback.String(),
		q.maxPasses, q.requireSettled, q.backoff.String())

	var s stranded
	err := row.Scan(&s.runID, &s.label, &s.sessionID, &s.repo, &s.claudeSes,
		&s.reason, &s.summary, &s.lastFile, &s.pass, &s.startedAt, &s.endedAt, &s.finishSes)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r pgRows) ClaimSettle(ctx context.Context, q settleParams) (*candidate, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	row := r.pool.QueryRow(ctx, `
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
	`, codingKinds, q.lookback.String(), q.maxTries, q.each.String(),
		q.stallAfter.String(), q.settleGrace.String())

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

func (r pgRows) Note(ctx context.Context, runID, key, value string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET meta = jsonb_set(COALESCE(meta,'{}'::jsonb), ARRAY[$2], to_jsonb($3::text), true)
		 WHERE id = $1::uuid`, runID, key, value)
	return err
}

func (r pgRows) Spend(ctx context.Context, runID string, maxPasses int) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object('finish_passes', $2::int)
		 WHERE id = $1::uuid`, runID, maxPasses)
	return err
}

func (r pgRows) CloseCompleted(ctx context.Context, runID, status, summary string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
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
		 WHERE id = $1::uuid`, runID, status, summary)
	return err
}

func (r pgRows) CloseDead(ctx context.Context, runID, summary string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
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
		   AND status = 'running'`, runID, summary)
	return err
}

// EnsureContinuationSession opens the side session a stranded run is
// continued in.
//
// It is a real chat session (kind 'user', a fixed name, auto_named FALSE so
// the namer never retitles it) because Jarvis's decision has to be somewhere
// the boss can read it, and the sessions list only offers sessions with a
// rendered conversation. It inherits the parent chat's project, project path
// and bridge preference so `code_agent` and the evidence gatherer target the
// same repo on the same machine as the job that stranded. origin_ref records
// the lineage so the row can be traced back to the run and the chat it came
// from. ON CONFLICT DO NOTHING: the second and third pass of the same run
// reuse the row, and the turn store's own upsert only touches last_run_at.
func (r pgRows) EnsureContinuationSession(ctx context.Context, sessionID, parentSessionID, name, runID string) error {
	if r.pool == nil {
		return errNoPool
	}
	origin, err := json.Marshal(map[string]any{
		"kind":              "continuation",
		"run_id":            runID,
		"parent_session_id": parentSessionID,
	})
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO mem_sessions (id, kind, name, auto_named, origin_ref,
		                          project, project_path, bridge_preference,
		                          started_at, last_run_at)
		SELECT $1::uuid, 'user', $3, FALSE, $4::jsonb,
		       p.project, p.project_path, p.bridge_preference,
		       NOW(), NOW()
		  FROM (SELECT $2::uuid AS id) want
		  LEFT JOIN mem_sessions p ON p.id = want.id
		ON CONFLICT (id) DO NOTHING
	`, sessionID, parentSessionID, name, string(origin))
	return err
}

// ActivePlanID mirrors plan.Store.GetActiveBySession's selection so the
// brief can hand the model the exact plan id to `plan_get` / `plan_resume`
// from the side session (those tools take a plan id, not a session id).
func (r pgRows) ActivePlanID(ctx context.Context, sessionID string) (string, error) {
	if r.pool == nil {
		return "", errNoPool
	}
	if !isChatSession(sessionID) {
		return "", nil
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_plans
		 WHERE session_id = $1::uuid AND status IN ('active','paused')
		 ORDER BY updated_at DESC LIMIT 1
	`, sessionID).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return id, err
}
