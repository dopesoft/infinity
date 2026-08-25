package sessions

import (
	"context"
	"log"
	"strings"
	"time"
)

// sweep.go — the guarantee behind session titles.
//
// Naming used to be a single best-effort shot fired at the end of a turn, and
// it silently missed in several ways that nothing ever retried:
//
//   - The row isn't there yet. MaybeName reads mem_sessions to check for an
//     existing name; the row is created asynchronously by the capture pipeline
//     on the first observation. Lose that race and MaybeName returns with the
//     comment "we'll try again next turn". For a chat there IS a next turn. For
//     a scheduled run there is exactly one turn ever, so "next turn" never
//     comes and the session stays nameless forever.
//   - The draft call failed. Logged, then dropped. No retry.
//   - The turn ended down a path that doesn't reach the namer at all.
//
// Every one of those failures looked like nothing at all: no error surfaced, no
// counter moved, and the boss just saw another hex slug in his history. Three
// months of them accumulated.
//
// So naming is now defended three deep:
//
//	1. the turn-end attempt (fast path, unchanged)
//	2. THIS sweep, which finds anything the fast path missed and names it,
//	   however long after the fact, with a bounded number of attempts
//	3. a deterministic display label in the API, so even with the model
//	   completely unavailable a row reads "Inbox triage" and never a uuid
//
// Layer 2 is what makes it a guarantee instead of a hope.

const (
	// maxNameAttempts caps LLM retries per session. A session that fails this
	// many times has something wrong with its content, not with the weather;
	// the deterministic label in the API carries it from there.
	maxNameAttempts = 3
	// DefaultSweepBatch is how many sessions one pass will title.
	DefaultSweepBatch = 20
)

// SweepResult is what one pass did. Real counts, so the caller can print an
// honest line instead of "done".
type SweepResult struct {
	Scanned   int `json:"scanned"`
	Named     int `json:"named"`
	Failed    int `json:"failed"`
	Remaining int `json:"remaining"`
}

// SweepUnnamed titles sessions the turn-end path missed.
//
// Only sessions with something to render are considered: a bookkeeping
// container with no conversation has nothing for a model to read, is hidden
// from the boss's list by the same predicate, and would just burn a call.
func (n *Namer) SweepUnnamed(ctx context.Context, limit int) (SweepResult, error) {
	var res SweepResult
	if n == nil || n.pool == nil || n.provider == nil {
		return res, nil
	}
	if limit <= 0 {
		limit = DefaultSweepBatch
	}

	rows, err := n.pool.Query(ctx, `
		SELECT s.id::text,
		       COALESCE((SELECT o.raw_text FROM mem_observations o
		                  WHERE o.session_id = s.id
		                    AND o.hook_name IN ('UserPromptSubmit', 'DashboardSeed')
		                    AND btrim(COALESCE(o.raw_text,'')) <> ''
		                  ORDER BY o.created_at LIMIT 1), '') AS opening,
		       COALESCE((SELECT o.raw_text FROM mem_observations o
		                  WHERE o.session_id = s.id
		                    AND o.hook_name = 'TaskCompleted'
		                    AND btrim(COALESCE(o.raw_text,'')) <> ''
		                  ORDER BY o.created_at DESC LIMIT 1), '') AS reply
		  FROM mem_sessions s
		 WHERE s.deleted_at IS NULL
		   AND s.name IS NULL
		   AND COALESCE((s.metadata->>'name_attempts')::int, 0) < $1
		   AND `+HasRenderableSQL+`
		 ORDER BY COALESCE(s.last_run_at, s.started_at) DESC
		 LIMIT $2
	`, maxNameAttempts, limit)
	if err != nil {
		return res, err
	}
	type target struct{ id, opening, reply string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.opening, &t.reply); err != nil {
			continue
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	res.Scanned = len(targets)

	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		// Nothing readable to title from. Count the attempt so a session that
		// can never be named stops being picked up on every pass.
		if strings.TrimSpace(t.opening) == "" && strings.TrimSpace(t.reply) == "" {
			n.countNameAttempt(ctx, t.id, "no readable content")
			res.Failed++
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		name, err := n.draftName(dctx, firstNonEmptyStr(t.opening, t.reply), t.reply)
		cancel()
		if err != nil || strings.TrimSpace(name) == "" {
			reason := "empty draft"
			if err != nil {
				reason = err.Error()
			}
			// A failure to name is a real failure of our own code path, not
			// noise: record it on the row and say so on stderr, so a namer
			// that has quietly stopped working is visible instead of just
			// producing more hex slugs.
			n.countNameAttempt(ctx, t.id, reason)
			log.Printf("sessions.sweep: could not title session=%s: %s", t.id, truncate(reason, 200))
			res.Failed++
			continue
		}
		if _, err := n.pool.Exec(ctx,
			`UPDATE mem_sessions SET name = $2, auto_named = TRUE WHERE id = $1::uuid AND name IS NULL`,
			t.id, name); err != nil {
			log.Printf("sessions.sweep: write session=%s: %v", t.id, err)
			res.Failed++
			continue
		}
		res.Named++
	}

	if err := n.pool.QueryRow(ctx, `
		SELECT count(*) FROM mem_sessions s
		 WHERE s.deleted_at IS NULL AND s.name IS NULL
		   AND COALESCE((s.metadata->>'name_attempts')::int, 0) < $1
		   AND `+HasRenderableSQL+`
	`, maxNameAttempts).Scan(&res.Remaining); err != nil {
		res.Remaining = -1 // unknown; never report a zero we didn't measure
	}
	if res.Named > 0 {
		infoLog.Printf("sessions.sweep: titled %d of %d sessions (%d failed, %d still untitled)",
			res.Named, res.Scanned, res.Failed, res.Remaining)
	}
	return res, nil
}

// countNameAttempt records a failed titling attempt on the session so the sweep
// gives up after maxNameAttempts, and keeps the reason where it can be read.
func (n *Namer) countNameAttempt(ctx context.Context, sessionID, reason string) {
	if _, err := n.pool.Exec(ctx, `
		UPDATE mem_sessions
		   SET metadata = COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
		       'name_attempts', COALESCE((metadata->>'name_attempts')::int, 0) + 1,
		       'name_error', $2::text,
		       'name_last_try', NOW()::text)
		 WHERE id = $1::uuid
	`, sessionID, truncate(reason, 300)); err != nil {
		log.Printf("sessions.sweep: stamp session=%s: %v", sessionID, err)
	}
}

// StartSweep runs the sweep on a ticker for the life of the process. This is
// the LIVE PATH: without it the sweep is code nothing calls, which is the same
// as not having written it.
func StartSweep(ctx context.Context, n *Namer, every time.Duration, batch int) {
	if n == nil || n.pool == nil || n.provider == nil {
		return
	}
	if every <= 0 {
		every = 10 * time.Minute
	}
	go func() {
		// A short delay so boot finishes before the first pass.
		t := time.NewTimer(90 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if _, err := n.SweepUnnamed(ctx, batch); err != nil {
				log.Printf("sessions.sweep: %v", err)
			}
			t.Reset(every)
		}
	}()
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
