// Package finish makes a coding job that stopped short get picked back up.
//
// THE FAILURE THIS EXISTS TO CLOSE. Plan 2f1508a2, still sitting paused: step 0
// `failed`, step 1 `skipped`, and steps 2 and 3 — "Add worker liveness,
// checkpoint, and automatic continuation safeguards" and "Enforce proof-gated
// completion" — never started. The plan to fix this was killed by the thing it
// was fixing, and then nothing picked it up. The boss had to come back and ask.
//
// Everything before this package made the STOPPING honest: a job outlives the
// turn, an interruption is no longer laundered into a failure, the transcript
// is salvaged, Claude's own session id is captured. All of which leaves a run
// row that says, accurately, "this never reached a verdict" — and no one
// reading it.
//
// This is the reader. Deterministic Go on a ticker (never LLM cognition):
// it finds a coding run that ended WITHOUT a verdict, gathers real evidence off
// the repo, and re-enters the agent loop in the originating chat with that
// evidence in hand. Jarvis then makes the only call that is genuinely a
// judgment — continue it, replan it, or say it needs the boss — and he can
// continue it cheaply because `code_agent` now takes resume_session.
//
// The split is deliberate and is CLAUDE.md Rule #1b. Mechanics here, in code,
// where they cannot be forgotten: noticing, gathering the evidence, capping the
// passes, claiming a pass atomically so a crash can't loop. Judgment there, in
// the model: what to do about it.
//
// Modelled on reauth.Poller and watch.Poller — same shape, same durability
// story (state lives on the row, so a restart resumes on the next tick), same
// Replayer seam back into agent.Loop.Run.
package finish

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog routes lifecycle lines to stdout so Railway tags them severity:info
// rather than the stderr default (severity:error). See CLAUDE.md → "Logging".
var infoLog = log.New(os.Stdout, "finish: ", log.LstdFlags)

// Replayer re-enters the agent loop for a session and returns the assistant's
// final text. Satisfied by the same adapter the reauth poller uses, so there is
// one implementation of "wake Jarvis up in this chat", not two.
type Replayer interface {
	Replay(ctx context.Context, sessionID, userText, model string) (string, error)
}

// Report is what the repo actually looks like right now. It is the difference
// between "the job stopped" and "the job stopped having written six files" —
// the second is a continuation, the first may be a replan.
type Report struct {
	Repo     string
	Branch   string
	Head     string
	Dirty    []string // paths with uncommitted changes
	DiffStat string
	// Err is why the evidence could not be gathered. Populated instead of
	// leaving the report silently empty, because an empty report that means
	// "I could not look" must never read as "there was nothing there"
	// (CLAUDE.md → "Empty-because-broken must never read as empty-because-fine").
	Err string
}

// Gathered reports whether the evidence is real rather than a failed probe.
func (r Report) Gathered() bool { return r.Err == "" }

// Evidence reads the repo's state off whichever bridge serves that session.
// An interface rather than a bridge dependency so this package stays testable
// with no Mac, no cloud and no network.
type Evidence interface {
	Gather(ctx context.Context, sessionID, repo string) Report
}

// Poller is the deterministic background loop. Nil-safe throughout.
type Poller struct {
	pool     *pgxpool.Pool
	replayer Replayer
	evidence Evidence

	interval time.Duration
	// settleGrace is how long after a run closes we wait before acting, so
	// the follower, the watch poller and the plan reconciler have all had
	// their say and we are reacting to a settled picture.
	settleGrace time.Duration
	// lookback bounds how far back a stranded run is still worth continuing.
	// Past this the world has moved on and a surprise build is not a favour.
	lookback time.Duration
	// stallAfter is how long a RUNNING job may show no new activity before
	// its row stops claiming to be busy. Generous on purpose: one `Bash`
	// step running `go test ./...` is a single activity for its whole
	// duration, and a healthy test run must never be called stalled.
	stallAfter time.Duration
	maxPasses  int
}

// NewPoller returns a Poller, or nil when a dependency is missing (chat-only
// or migrate-only processes then simply have no continuation loop).
// evidence may be nil: the continuation still happens, and says plainly that
// it went in without a look at the repo.
func NewPoller(pool *pgxpool.Pool, replayer Replayer, evidence Evidence) *Poller {
	if pool == nil || replayer == nil {
		return nil
	}
	return &Poller{
		pool:        pool,
		replayer:    replayer,
		evidence:    evidence,
		interval:    60 * time.Second,
		settleGrace: 2 * time.Minute,
		lookback:    3 * time.Hour,
		stallAfter:  12 * time.Minute,
		maxPasses:   3,
	}
}

// Start runs the tick loop until ctx is cancelled. All state lives on the run
// rows, so a restart resumes on the next tick with no recovery path.
func (p *Poller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	infoLog.Printf("continuation poller started (every %s, stall after %s, max %d passes)",
		p.interval, p.stallAfter, p.maxPasses)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	// Truth first: a job that has gone quiet stops claiming to be busy before
	// anything else happens, because that is what the boss is looking at.
	if n, err := p.MarkStalled(ctx); err != nil {
		log.Printf("finish: marking stalled runs: %v", err)
	} else if n > 0 {
		infoLog.Printf("marked %d run(s) as showing no new activity", n)
	}
	if err := p.ContinueOne(ctx); err != nil {
		log.Printf("finish: continuation pass: %v", err)
	}
}

// lastActivitySQL is when the run last did something NEW. progress_label is
// useless for this: it embeds elapsed time, so it changes every 15s whether or
// not Claude did anything. meta.activity_at moves only on a new tool+target.
// The regex guard means one malformed value can't error the whole query out
// every tick — Postgres has no TRY_CAST.
const lastActivitySQL = `CASE WHEN meta->>'activity_at' ~ '^\d{4}-\d{2}-\d{2}T'
                              THEN (meta->>'activity_at')::timestamptz
                              ELSE started_at END`

// passesSQL is how many continuations this run has already had, guarded the
// same way.
const passesSQL = `CASE WHEN meta->>'finish_passes' ~ '^[0-9]+$'
                        THEN (meta->>'finish_passes')::int ELSE 0 END`

// codingKinds are the run kinds that mean "Claude Code working in a repo".
// Taken from the runs package rather than re-listed here: the reaper, the
// stranded-run recovery and this poller all have to agree about what counts as
// coding work, and a second copy of the list is how they stop agreeing.
var codingKinds = runs.CodingKinds()

// MarkStalled makes a running job that has shown no new activity say so, and
// returns how many rows it changed.
//
// It deliberately does NOT kill anything. The one stall signature available —
// no new tool call for N minutes — is indistinguishable from a long, healthy
// `go build` or `go test`, and killing a working test run is a worse failure
// than waiting out the ceiling the job already has. The job's own deadline
// still ends it; this exists so the dock above the composer stops showing a
// confident progress bar for something that has not moved in ten minutes.
// "Still spinning is not a status."
func (p *Poller) MarkStalled(ctx context.Context) (int, error) {
	if p == nil || p.pool == nil {
		return 0, nil
	}
	tag, err := p.pool.Exec(ctx, `
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
	`, codingKinds, p.stallAfter.String())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// stranded is one coding run that ended without ever reaching a verdict.
type stranded struct {
	runID     string
	label     string
	sessionID string
	repo      string
	claudeSes string
	reason    string
	summary   string
	lastFile  string
	pass      int
	startedAt time.Time
	endedAt   time.Time
}

// ContinueOne claims at most one stranded run and continues it.
//
// One per tick on purpose: a continuation is a full agent turn on the boss's
// brain, so this is a drip, never a burst. Nothing to do is the overwhelmingly
// common case and costs one indexed query.
func (p *Poller) ContinueOne(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return nil
	}
	s, err := p.claim(ctx)
	if err != nil || s == nil {
		return err
	}
	report := Report{Repo: s.repo, Err: "no evidence gatherer is wired, so I did not look at the repo"}
	if p.evidence != nil {
		report = p.evidence.Gather(ctx, s.sessionID, s.repo)
	}
	brief := buildBrief(*s, report, p.maxPasses)

	infoLog.Printf("continuing stranded run %s (pass %d/%d) in session %s", s.runID, s.pass, p.maxPasses, s.sessionID)
	if _, err := p.replayer.Replay(ctx, s.sessionID, brief, ""); err != nil {
		// The pass is already spent — deliberately. Retrying a replay that
		// just failed is the "stop retrying something dead" anti-pattern, and
		// a crash between the claim and here must not be able to loop.
		p.note(ctx, s.runID, "finish_error", err.Error())
		return fmt.Errorf("replay run %s into session %s: %w", s.runID, s.sessionID, err)
	}
	p.note(ctx, s.runID, "finish_outcome", "continued")
	return nil
}

// claim atomically takes the next stranded run and books its pass in the same
// statement, so two ticks (or two boxes) can never continue the same job twice
// and a crash after the claim costs one pass rather than an infinite loop.
//
// Only runs with NO VERDICT are eligible. A run that closed 'error' failed for
// a reason — a bad repo, no subscription, a spent plan — and relaunching it is
// exactly the "stop retrying something dead" behaviour the honesty rules ban.
func (p *Poller) claim(ctx context.Context) (*stranded, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE mem_runs SET meta = COALESCE(meta,'{}'::jsonb) || jsonb_build_object(
		         'finish_passes',  (`+passesSQL+`) + 1,
		         'finish_last_at', to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
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
		      AND COALESCE(r.meta->>'session_id','') <> ''
		      AND COALESCE(r.meta->>'repo','')       <> ''
		      -- Never while newer coding work is live in the same chat: that
		      -- job may BE the continuation, and two drivers on one repo is
		      -- how the same edit gets made twice.
		      AND NOT EXISTS (
		            SELECT 1 FROM mem_runs live
		             WHERE live.kind = ANY($1)
		               AND live.status = 'running'
		               AND live.meta->>'session_id' = r.meta->>'session_id')
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
		          (`+passesSQL+`),
		          started_at,
		          ended_at
	`, codingKinds, p.settleGrace.String(), p.lookback.String(), p.maxPasses)

	var s stranded
	err := row.Scan(&s.runID, &s.label, &s.sessionID, &s.repo, &s.claudeSes,
		&s.reason, &s.summary, &s.lastFile, &s.pass, &s.startedAt, &s.endedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A synthetic session (a delegate, a background job) has no chat to
	// deliver into and would be a 22P02 on every mem_* write besides.
	if !isChatSession(s.sessionID) {
		return nil, nil
	}
	return &s, nil
}

// note records an outcome on the run row. Best-effort: failing to annotate
// must never stop the continuation itself.
func (p *Poller) note(ctx context.Context, runID, key, value string) {
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := p.pool.Exec(nctx, `
		UPDATE mem_runs
		   SET meta = jsonb_set(COALESCE(meta,'{}'::jsonb), ARRAY[$2], to_jsonb($3::text), true)
		 WHERE id = $1::uuid`, runID, key, value); err != nil {
		log.Printf("finish: annotating run %s: %v", runID, err)
	}
}

// isChatSession reports whether the id is a real uuid chat session.
func isChatSession(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
