package sessions

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
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

	// Give back the budget that a broken brain took. Sessions condemned by a
	// model refusal BEFORE that stopped counting (see the branch below) still
	// carry a spent budget and would stay "New Conversation" forever, even
	// though the reason is long fixed. This drains that backlog once and then
	// finds nothing, because such failures no longer increment.
	if freed := n.reclaimModelRefusals(ctx); freed > 0 {
		infoLog.Printf("sessions.sweep: freed %d session(s) condemned by a model refusal; they are titlable again", freed)
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
			// A spent plan says nothing about this session. Counting it
			// against the attempt budget let a few hours of quota exhaustion
			// permanently condemn a perfectly nameable conversation to a hex
			// slug - which is precisely what happened on 2026-08-30, when two
			// sessions burned all three attempts against an out-of-usage
			// ChatGPT plan. Record the reason so the failure stays visible,
			// but leave the budget alone so the next pass tries again.
			if _, isQuota := llm.AsQuota(err); isQuota {
				n.noteNameError(ctx, t.id, reason)
				log.Printf("sessions.sweep: session=%s deferred, the brain is out of usage: %s",
					t.id, truncate(reason, 200))
				res.Failed++
				continue
			}
			// A brain refusing the MODEL says nothing about this session
			// either, and for the same reason it must not spend the budget:
			// on 2026-08-29 titling was pointed at a model the boss's ChatGPT
			// account rejects outright ("codex-mini-latest is not supported
			// when using Codex with a ChatGPT account"), which is a 400 and
			// not a quota error - so every session opened over the next two
			// days burned all three attempts against a configuration mistake
			// and was condemned to read "New Conversation" permanently. A
			// misconfiguration must never be able to mark a nameable
			// conversation unnameable.
			if llm.IsUnsupportedModel(reason) {
				n.noteNameError(ctx, t.id, reason)
				log.Printf("sessions.sweep: session=%s deferred, the brain refused the model: %s",
					t.id, truncate(reason, 200))
				res.Failed++
				continue
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

// reclaimModelRefusals zeroes the attempt budget on sessions whose only
// recorded failure was a brain refusing the model.
//
// The candidates are read and then filtered in GO, through the same
// llm.IsUnsupportedModel the failure path uses, rather than re-expressing that
// predicate as a pile of SQL ILIKEs. One definition, so the thing that spends
// the budget and the thing that refunds it can never disagree about what a
// model refusal is.
//
// name_error is deliberately left in place: the row keeps saying what went
// wrong, it just stops being punished for it.
func (n *Namer) reclaimModelRefusals(ctx context.Context) int {
	rows, err := n.pool.Query(ctx, `
		SELECT id::text, COALESCE(metadata->>'name_error', '')
		  FROM mem_sessions
		 WHERE deleted_at IS NULL
		   AND name IS NULL
		   AND COALESCE((metadata->>'name_attempts')::int, 0) > 0
		   AND COALESCE(metadata->>'name_error', '') <> ''
		 LIMIT 500
	`)
	if err != nil {
		log.Printf("sessions.sweep: reclaim scan: %v", err)
		return 0
	}
	var ids []string
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			continue
		}
		if llm.IsUnsupportedModel(reason) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("sessions.sweep: reclaim scan: %v", err)
	}
	if len(ids) == 0 {
		return 0
	}
	if _, err := n.pool.Exec(ctx, `
		UPDATE mem_sessions
		   SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('name_attempts', 0)
		 WHERE id = ANY($1::uuid[])
	`, ids); err != nil {
		log.Printf("sessions.sweep: reclaim update: %v", err)
		return 0
	}
	return len(ids)
}

// countNameAttempt records a failed titling attempt on the session so the sweep
// gives up after maxNameAttempts, and keeps the reason where it can be read.
// noteNameError records WHY a pass could not title a session without
// spending one of its attempts. For transient failures (a spent plan): the
// row still shows the real reason, and the next sweep retries.
func (n *Namer) noteNameError(ctx context.Context, sessionID, reason string) {
	if _, err := n.pool.Exec(ctx, `
		UPDATE mem_sessions
		   SET metadata = COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
		       'name_error', $2::text,
		       'name_last_try', NOW()::text)
		 WHERE id = $1::uuid
	`, sessionID, truncate(reason, 300)); err != nil {
		log.Printf("sessions.sweep: stamp session=%s: %v", sessionID, err)
	}
}

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
