package cron

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/inbox"
	"github.com/dopesoft/infinity/core/internal/maintenance"
)

var backfillInfoLog = log.New(os.Stdout, "", log.LstdFlags)

const backfillMarkerKey = "backfill_run_narratives_v1"

// BackfillRunNarratives is a ONE-SHOT boot sweep that rewrites the past week's
// cron run cards into the new human shape, so the boss can verify the fix on
// his EXISTING "Surfaced by Jarvis" items without waiting for tonight's runs:
//
//   - nightly-cognition runs: meta.report holds the full Report JSON and the
//     row holds started/ended — rebuild the digest with retroactive highlights
//     (the lessons/memories those runs created are still in the DB).
//   - inbox-triage runs: rebuild from the stored counters via Summary.Human().
//   - any run whose meta says failures>0 but outcome says did_work: re-stamp
//     stopped_early so the "DONE" chips on the broken self-improve nights
//     read honestly.
//
// Each cron's rolling inbox item is then re-posted from its most recent run
// through the same surfaceRunOutcome writer live runs use. Guarded by an
// infinity_meta marker so it runs exactly once; best-effort throughout.
func (s *Scheduler) BackfillRunNarratives(ctx context.Context) {
	if s == nil || s.pool == nil {
		return
	}
	var marked bool
	_ = s.pool.QueryRow(ctx,
		`SELECT TRUE FROM infinity_meta WHERE key = $1`, backfillMarkerKey).Scan(&marked)
	if marked {
		return
	}

	rows, err := s.pool.Query(ctx, `
		SELECT r.id::text, COALESCE(r.target_id, ''), COALESCE(c.name, ''),
		       r.started_at, COALESCE(r.ended_at, r.started_at),
		       COALESCE(r.result_summary, ''), COALESCE(r.meta, '{}'::jsonb),
		       COALESCE(r.status, '')
		  FROM mem_runs r
		  LEFT JOIN mem_crons c ON c.id::text = r.target_id
		 WHERE r.kind = 'cron'
		   AND r.started_at > NOW() - INTERVAL '7 days'
		 ORDER BY r.started_at ASC
	`)
	if err != nil {
		log.Printf("backfill run narratives: query: %v", err)
		return
	}

	type runRow struct {
		id, cronID, cronName, summary, status string
		started, ended                        time.Time
		meta                                  map[string]any
	}
	var runs []runRow
	for rows.Next() {
		var rr runRow
		var metaRaw []byte
		if err := rows.Scan(&rr.id, &rr.cronID, &rr.cronName, &rr.started, &rr.ended,
			&rr.summary, &metaRaw, &rr.status); err != nil {
			continue
		}
		_ = json.Unmarshal(metaRaw, &rr.meta)
		runs = append(runs, rr)
	}
	rows.Close()

	rewritten := 0
	latestByCron := map[string]runRow{}
	for _, rr := range runs {
		newSummary, changed := s.backfillSummary(ctx, rr.meta, rr.started, rr.ended, rr.summary)
		newOutcome := backfillOutcome(rr.meta)
		if changed || newOutcome != "" {
			set := map[string]any{}
			if newOutcome != "" {
				set["outcome"] = string(newOutcome)
			}
			patch, _ := json.Marshal(set)
			if _, err := s.pool.Exec(ctx, `
				UPDATE mem_runs
				   SET result_summary = $2,
				       meta = COALESCE(meta, '{}'::jsonb) || $3::jsonb
				 WHERE id = $1::uuid
			`, rr.id, newSummary, string(patch)); err == nil {
				rewritten++
			}
			rr.summary = newSummary
			if newOutcome != "" {
				rr.meta["outcome"] = string(newOutcome)
			}
		}
		if rr.cronID != "" {
			latestByCron[rr.cronID] = rr // started_at ASC → last write wins
		}
	}

	// Re-post each cron's rolling inbox item from its most recent run through
	// the SAME writer live runs use, so old cards pick up the new shape
	// (digest body, honest outcome label, no duplicated text).
	for cronID, rr := range latestByCron {
		if rr.cronName == "" {
			continue
		}
		outcome := Outcome(strFromMeta(rr.meta, "outcome"))
		if !outcome.valid() {
			continue
		}
		j := Job{ID: cronID, Name: rr.cronName, RunSessionID: strFromMeta(rr.meta, "session_id")}
		s.surfaceRunOutcome(ctx, j, RunSummary{Summary: rr.summary, Meta: rr.meta}, nil, outcome)
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO infinity_meta (key, value, updated_at)
		VALUES ($1, '{"done":true}'::jsonb, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, backfillMarkerKey); err != nil {
		log.Printf("backfill run narratives: marker: %v", err)
	}
	backfillInfoLog.Printf("backfill run narratives: rewrote %d run(s), refreshed %d inbox card(s)", rewritten, len(latestByCron))
}

// backfillSummary rebuilds a stored run's result_summary in the new human
// shape when the run's meta carries enough to do it deterministically.
func (s *Scheduler) backfillSummary(ctx context.Context, meta map[string]any, started, ended time.Time, current string) (string, bool) {
	if meta == nil {
		return current, false
	}
	// Nightly cognition: meta.report is the full Report JSON.
	if rawRep, ok := meta["report"]; ok {
		buf, err := json.Marshal(rawRep)
		if err != nil {
			return current, false
		}
		var rep maintenance.Report
		if json.Unmarshal(buf, &rep) != nil {
			return current, false
		}
		rep.Highlights = maintenance.CollectHighlights(ctx, s.pool, started, ended)
		rep.Digest = "" // no LLM in the backfill; deterministic lead
		return rep.Summary(), true
	}
	// Inbox triage: counters only (sender/subject examples start with the
	// next live run).
	if _, ok := meta["fetched"]; ok {
		sum := inbox.Summary{
			Accounts: intFromMetaOr(meta, "accounts"),
			Fetched:  intFromMetaOr(meta, "fetched"),
			Surfaced: intFromMetaOr(meta, "surfaced"),
		}
		return sum.Human(), true
	}
	return current, false
}

// backfillOutcome returns the corrected outcome for a stored run, or "" when
// the stored one stands. Mirrors classifyOutcome's new precedence: visible
// failures can never read "did_work".
func backfillOutcome(meta map[string]any) Outcome {
	if meta == nil {
		return ""
	}
	if Outcome(strFromMeta(meta, "outcome")) != OutcomeDidWork {
		return ""
	}
	if intFromMetaOr(meta, "failures") > 0 || intFromMetaOr(meta, "abandoned_steps") > 0 {
		return OutcomeStoppedEarly
	}
	return ""
}

func strFromMeta(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, _ := meta[key].(string)
	return v
}

func intFromMetaOr(meta map[string]any, key string) int {
	n, _ := intFromMeta(meta, key)
	return n
}
