package runs

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LiveCodingRun answers "is a coding job ALREADY running for this chat?".
//
// 2026-08-29, session 0ac9a94c: a `background_build` launched at 05:14:07 and a
// `code_agent` at 05:15:12 — two Claude Code jobs, one task, both editing the
// same repo. Jarvis then tried to referee them by hand, shelling out to
// `kill <pid>` and a `while [ ! -f … ]; do sleep 2; done` wait loop that ran for
// a quarter of an hour while the boss watched a spinner.
//
// BackgroundAgent had an in-process guard (one build per session), but it was
// in-process and per-tool: it could not see a `code_agent` run, `code_agent`
// had no guard at all, and neither survives a restart. The durable answer is
// the run table, which both engines already write to — so this is the one
// question both of them ask before launching anything.
func LiveCodingRun(ctx context.Context, pool *pgxpool.Pool, sessionID string) (runID, label string, found bool) {
	if pool == nil || strings.TrimSpace(sessionID) == "" {
		return "", "", false
	}
	err := pool.QueryRow(ctx, `
		SELECT id::text, label
		  FROM mem_runs
		 WHERE kind = ANY($1)
		   AND status = 'running'
		   AND meta->>'session_id' = $2
		 ORDER BY started_at DESC
		 LIMIT 1
	`, CodingKinds(), sessionID).Scan(&runID, &label)
	switch {
	case err == nil:
		return runID, label, true
	case errors.Is(err, pgx.ErrNoRows):
		// The common answer: nothing is running, go ahead.
		return "", "", false
	default:
		// We could not look. This opens the gate rather than closing it,
		// deliberately: a transient database blip must not block the boss's
		// coding entirely, and a duplicate job is recoverable where "you
		// cannot code right now" is not. It is NOT swallowed — the failure of
		// our own query is named on stderr so it can be seen and fixed, per
		// the no-false-greens rule.
		log.Printf("runs: could not check for a live coding run in session %s, allowing the launch: %v", sessionID, err)
		return "", "", false
	}
}
