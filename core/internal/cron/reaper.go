package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/runs"
)

// interruptedNarrative is the first-person result for a cron run whose process
// vanished before it could finalize — most often because the nightly
// self-improve loop pushed code, Railway redeployed the core service, and the
// SIGTERM killed the very container running the job. The honest outcome label
// (stopped early / done / needs you) is derived separately from the recorded
// signal; this is just the "why there's no live narrative" line.
const interruptedNarrative = "This run was interrupted before it could wrap up — the box restarted mid-run (most likely a deploy from my own push). " +
	"I recovered the result from what was recorded; I'll confirm the rest on the next pass."

// codingReconcileFloor is the minimum grace before a DETACHED coding run
// (background.build / code_agent) is reconciled. It has to comfortably exceed
// the longest legitimate job — background_build's 60-minute ceiling — because
// these kinds are exempt from the blanket 45-minute run reaper precisely so a
// wall clock can't call a working build dead. Two hours is "the process that
// owned this is definitely gone", not "it's taking a while".
const codingReconcileFloor = 2 * time.Hour

// ReconcileAges maps a mem_runs kind to the minimum age a stranded run of that
// kind must reach before ReconcileReaped will re-derive its outcome. Per-kind
// because the safe grace genuinely differs: a cron must outlive its 30-minute
// job timeout, a plan.step is stranded within minutes (and has its own tighter
// reaper racing it), and a detached coding job legitimately runs for an hour.
type ReconcileAges map[string]time.Duration

// DefaultReconcileAges is the standard scope: crons and coding jobs on their own
// budgets, plan steps on the tighter step budget.
//
// stepAge MUST be at or below the plan.step reaper's age (serve.go's
// stepReapAge). The two race on the same rows and the reaper stamps a bare
// 'error'; reconciling first is what makes the honest classification actually
// reachable rather than dead code that always loses (CLAUDE.md Rule #1c).
func DefaultReconcileAges(cronAge, stepAge time.Duration) ReconcileAges {
	codingAge := codingReconcileFloor
	if cronAge > codingAge {
		codingAge = cronAge
	}
	ages := ReconcileAges{
		string(runs.KindCron):     cronAge,
		string(runs.KindPlanStep): stepAge,
	}
	for _, k := range runs.CodingKinds() {
		ages[k] = codingAge
	}
	return ages
}

// ReconcileReaped gives a CLEAR boss-facing outcome to every run that was
// orphaned — its container died (a self-push redeploy SIGTERM, a crash, an OOM)
// or its finalize block never ran — instead of leaving it as a silent stuck
// 'running' spinner or a bare 'error' with no story. This is the safety net
// behind makeFireFn's fresh-context finalize: when even that 30s window never
// runs because the process is already gone, the next boot / reaper tick
// reconciles the row here so the kanban card and the inbox still tell the truth.
//
// It covers FOUR kinds now, not just 'cron' (see ReconcileAges): 'cron',
// 'plan.step', and the two detached coding kinds. They all reached here the same
// way — orphaned with no receipt — and the generic sweeps stamped all of them
// 'error', which the plan layer then read as proof the work had FAILED. Same
// re-derivation, same classifier, no second implementation: every kind goes
// through classifyOutcome (via finalizeOutcome), so the hard-HTTP-failure veto
// that stops false greens applies identically to all of them.
//
// The cron-only bookkeeping (the rolling inbox card, the mem_crons last-run row)
// stays cron-only — a plan step is not a cron and must not pretend to be one.
//
// It is IDEMPOTENT and ORDER-INDEPENDENT: it targets runs that lack a stamped
// meta.outcome (the live finalize ALWAYS stamps one), regardless of whether a
// generic sweep (runs.RecoverStranded / runs.ReapTimedOut) has already flipped
// the row. So it can run before OR after those sweeps and still add the outcome
// they omit. The meta-guard makes a second pass a no-op.
//
// The per-kind minAge excludes recently-started runs so a healthy in-flight run
// is never clobbered, and plan.step rows carry the same in-flight-turn gate as
// runs.ReapTimedOutKind — age alone is a lie for a step inside a live turn (the
// 2026-07-02 "Research the market ❌ while the turn was still working" incident).
// Returns the number of runs reconciled.
func (s *Scheduler) ReconcileReaped(ctx context.Context, ages ReconcileAges) (int, error) {
	if s == nil || s.pool == nil || len(ages) == 0 {
		return 0, nil
	}
	total := 0
	for kind, minAge := range ages {
		n, err := s.reconcileKind(ctx, kind, minAge)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// reconcileKindSQL selects orphaned runs of one kind that never got an outcome.
//
// The NOT EXISTS is the in-flight-turn gate, joined through the plan graph
// (run → mem_plan_steps.run_id → mem_plans → mem_turns on session_id). A
// plan.step whose session still has a live turn is NOT orphaned however old it
// is. A run with no plan-step backing doesn't match the join, so the clause is
// trivially true and every other kind behaves exactly as before.
//
// The same LEFT JOIN also recovers the run's session id from the plan when
// meta.session_id is absent (plan.step rows are booked without one) — without a
// session id classifyOutcome cannot run its HTTP-failure veto, and a run whose
// 401 we can't see would classify green. Recovering it keeps the veto armed.
//
// %s is stillOpenOnly for every kind EXCEPT cron. The meta.outcome guard alone
// bounds the cron set (its live finalize always stamps one) but bounds nothing
// else: no other kind ever stamps an outcome, so without the extra clause this
// would re-sweep every plan.step in history on every tick and stamp
// "interrupted" on runs that had finished perfectly well. Orphaned means the
// owning process never closed the row — that is exactly status='running'.
const reconcileKindSQL = `
	SELECT r.id::text, COALESCE(r.target_id,''), COALESCE(c.name,''),
	       COALESCE(c.schedule,''), COALESCE(c.job_kind,''),
	       COALESCE(r.meta,'{}'::jsonb), r.started_at,
	       COALESCE(pl.session_id::text, '')
	  FROM mem_runs r
	  LEFT JOIN mem_crons c ON c.id::text = r.target_id
	  LEFT JOIN mem_plan_steps st ON st.run_id = r.id
	  LEFT JOIN mem_plans pl ON pl.id = st.plan_id
	 WHERE r.kind = $2
	   AND (r.meta->>'outcome') IS NULL
	   %s
	   AND r.started_at < NOW() - ($1 * interval '1 second')
	   AND NOT EXISTS (
	       SELECT 1
	         FROM mem_plan_steps st2
	         JOIN mem_plans p2 ON p2.id = st2.plan_id
	         JOIN mem_turns tn ON tn.session_id = p2.session_id
	        WHERE st2.run_id = r.id
	          AND tn.status = 'in_flight'
	   )
	 ORDER BY r.started_at ASC`

// stillOpenOnly restricts a non-cron reconcile to rows nothing ever closed.
const stillOpenOnly = `AND r.status = 'running'`

func (s *Scheduler) reconcileKind(ctx context.Context, kind string, minAge time.Duration) (int, error) {
	isCron := kind == string(runs.KindCron)
	scope := stillOpenOnly
	if isCron {
		scope = ""
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(reconcileKindSQL, scope), minAge.Seconds(), kind)
	if err != nil {
		return 0, fmt.Errorf("reconcile reaped %s: query: %w", kind, err)
	}
	type row struct {
		id, cronID, name, schedule, jobKind string
		meta                                map[string]any
		started                             time.Time
		planSession                         string
	}
	var pending []row
	for rows.Next() {
		var rr row
		var metaRaw []byte
		if err := rows.Scan(&rr.id, &rr.cronID, &rr.name, &rr.schedule, &rr.jobKind, &metaRaw, &rr.started, &rr.planSession); err != nil {
			continue
		}
		_ = json.Unmarshal(metaRaw, &rr.meta)
		pending = append(pending, rr)
	}
	rows.Close()

	reconciled := 0
	for _, rr := range pending {
		if isCron && (rr.cronID == "" || rr.name == "") {
			continue // can't address a cron we can't name; the generic sweep still closed the row
		}
		sessionID := strFromMeta(rr.meta, "session_id")
		if sessionID == "" {
			sessionID = rr.planSession
		}
		j := Job{
			ID:           rr.cronID,
			Name:         rr.name,
			Schedule:     rr.schedule,
			JobKind:      JobKind(rr.jobKind),
			RunSessionID: sessionID,
		}
		// Classify from the REAL recorded signal (turns / plan completeness /
		// http failures) on this FRESH ctx, not a synthetic error: an interrupted
		// run that had actually finished its plan still reads did_work; one cut
		// off mid-plan reads stopped_early; one that hit a 401 reads failed.
		// The cron narrative names the usual cause (a self-push redeploy). For a
		// plan step or a detached coding job we genuinely don't know why the
		// process went away, so use the cause-agnostic line rather than asserting
		// a deploy that may not have happened.
		narrative := interruptedNarrative
		if !isCron {
			narrative = runs.InterruptedSummary(runs.StoppedInterrupted, "")
		}
		summary := RunSummary{Summary: narrative, Meta: rr.meta}
		outcome, summary, execErr := s.finalizeOutcome(ctx, j, summary, nil)
		if !isCron {
			outcome = reapedOutcomeFloor(outcome)
		}

		// Enrich the row in place — works whether it's still 'running' or was
		// already swept to 'error'. Status mirrors FinishByID: 'error' only for a
		// genuine failure; an interrupted-but-not-errored run closes 'ok' with its
		// honest stopped_early / did_work outcome label. The meta.outcome guard in
		// the WHERE makes this atomic + idempotent (a racing pass affects 0 rows).
		status := "ok"
		errStr := ""
		humanJSON := "{}"
		// stopped_reason is what stops plan.Store.ReconcileStranded reading this
		// row as proof the step failed. It is stamped ONLY when the classifier
		// did not find a failure — a run the veto marked failed stays a plain,
		// un-excused 'error' and still fails its step.
		patchFields := map[string]any{"outcome": string(outcome)}
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
		} else if !isCron {
			patchFields["stopped_reason"] = runs.StoppedInterrupted
		}
		patch, _ := json.Marshal(patchFields)
		// Cron keeps overwriting result_summary with its recovered narrative
		// (unchanged). For every other kind the boot sweep may already have
		// written a better line ("Last checkpoint: …"), so preserve it.
		summarySQL := `result_summary = $4`
		if !isCron {
			summarySQL = `result_summary = CASE WHEN COALESCE(result_summary,'') = '' THEN $4 ELSE result_summary END`
		}
		ct, uerr := s.pool.Exec(ctx, `
			UPDATE mem_runs
			   SET status = $2,
			       ended_at = COALESCE(ended_at, NOW()),
			       duration_ms = COALESCE(duration_ms,
			           LEAST(2147483647, GREATEST(0,
			               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
			       error = CASE WHEN $3 = '' THEN error ELSE $3 END,
			       `+summarySQL+`,
			       human_error = CASE WHEN human_error IS NULL OR human_error = '{}'::jsonb THEN $5::jsonb ELSE human_error END,
			       meta = COALESCE(meta,'{}'::jsonb) || $6::jsonb
			 WHERE id = $1::uuid
			   AND (meta->>'outcome') IS NULL
		`, rr.id, status, errStr, summary.Summary, humanJSON, string(patch))
		if uerr != nil || ct.RowsAffected() == 0 {
			continue // query error, or another pass already reconciled it
		}
		if isCron {
			// Post the rolling inbox card + update the cron's last-run bookkeeping
			// through the SAME writers the live fire path uses, so a redeploy-killed
			// run looks identical to one that finished cleanly. surfaceRunOutcome
			// skips connector polls itself (j.JobKind is set above).
			s.surfaceRunOutcome(ctx, j, summary, execErr, outcome)
			s.recordCronRun(ctx, j, execErr, time.Now().UTC().Sub(rr.started).Milliseconds())
		}
		reconciled++
	}
	return reconciled, nil
}

// reapedOutcomeFloor corrects classifyOutcome's optimistic DEFAULT for a run
// that left no receipt at all.
//
// classifyOutcome ends at did_work when nothing contradicts it, which is right
// for a cron (its meta carries turn counts and plan state to contradict with)
// and wrong for a plan.step or a detached coding job, whose meta is usually
// bare. "Did work" would then be a guess dressed as a verdict — a false green,
// the exact thing the honesty machinery exists to prevent.
//
// It only ever moves did_work → stopped_early. Every ESCALATION the classifier
// can reach is untouched and still wins: failed (including the hard-HTTP-failure
// veto), needs_you (a pending trust contract, both bridges down), and
// nothing_needed all pass through exactly as classified.
func reapedOutcomeFloor(o Outcome) Outcome {
	if o == OutcomeDidWork {
		return OutcomeStoppedEarly
	}
	return o
}
