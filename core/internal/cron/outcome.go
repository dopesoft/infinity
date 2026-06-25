// honesty-machinery: do-not-revert — the hard-HTTP-failure veto that stops false
// greens. See the CLAUDE.md self-healing hard rule + memory project_sentry_blind_revert.
// Auto-rollback paths (sentry, verify cron, revert-veto) MUST never revert this.

package cron

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hardHTTPFailureWhere is the WHERE clause selecting outbound-HTTP failures that
// must veto a "green" run — auth, rate-limit, timeout, transport error, any 5xx.
// Mirrors httpx.IsActionableStatus; kept as SQL so the check is a single set-based
// query. Plain 404/400 are recorded for visibility but don't auto-fail a run.
const hardHTTPFailureWhere = `(status = 0 OR status IN (401,403,407,408,429) OR status >= 500)`

// Outcome is the boss-facing classification of a finished scheduled run. It is
// the single field the dashboard reads to answer, at a glance, "what happened
// last night?" — derived DETERMINISTICALLY (no LLM) so it can never be dropped
// the way a sentence in a skill can (CLAUDE.md Rule #1b: this is a mechanic).
type Outcome string

const (
	// OutcomeFailed — the run errored. Needs the boss's eyes; pings.
	OutcomeFailed Outcome = "failed"
	// OutcomeNeedsYou — a real decision is pending (a queued trust contract).
	// The ONLY class that lands in the board's "needs you" column.
	OutcomeNeedsYou Outcome = "needs_you"
	// OutcomeNothingNeeded — the run looked and there was nothing to do.
	OutcomeNothingNeeded Outcome = "nothing_needed"
	// OutcomeStoppedEarly — the agent ended its turn with work left undone and
	// no decision pending (e.g. it deliberately chose not to act). Informational.
	OutcomeStoppedEarly Outcome = "stopped_early"
	// OutcomeDidWork — the run did its job. Informational.
	OutcomeDidWork Outcome = "did_work"
)

func (o Outcome) valid() bool {
	switch o {
	case OutcomeFailed, OutcomeNeedsYou, OutcomeNothingNeeded, OutcomeStoppedEarly, OutcomeDidWork:
		return true
	}
	return false
}

// bridgeBlockedNarrative is the canonical run summary emitted whenever both
// Mac and cloud bridges were down during a run. It replaces whatever the agent
// wrote as its closing message so that neither the mem_runs result_summary nor
// the inbox card ever surfaces misleading success wording ("healthy", "Ran it",
// "logs look clean") when no workspace access was available to verify anything.
// Rule #1b: this is a mechanic — it must be guaranteed by code, never droppable
// skill prose.
const bridgeBlockedNarrative = "Workspace was unreachable (both Mac and cloud bridges were down) during this run.\n\n" +
	"Deploy state: unconfirmed — Railway logs and git history were inaccessible.\n" +
	"Log check: skipped — no bridge to read from.\n" +
	"Revert actions: intentionally skipped — git requires workspace access that was unavailable.\n\n" +
	"Bring a bridge back online and re-run to get a confirmed result."

// bridgeUnavailableInSession reports whether any observation in the given
// session carries the stable 'bridge_unavailable' token, meaning both Mac and
// cloud bridges were down when the run executed. Returns false when pool is nil
// or sessionID is empty (safe to call from unit tests with nil pool).
func bridgeUnavailableInSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) bool {
	if pool == nil || sessionID == "" {
		return false
	}
	var hit int
	return pool.QueryRow(ctx, `
		SELECT 1 FROM mem_observations
		 WHERE session_id = $1::uuid AND raw_text LIKE '%bridge_unavailable%'
		 LIMIT 1
	`, sessionID).Scan(&hit) == nil
}

// classifyOutcome distills a finished run into one Outcome. Priority order:
//  1. execErr present                       → failed
//  2. executor already declared an outcome  → trust it (system tasks know best)
//  3. a trust contract is awaiting the boss → needs_you
//  4. the run produced zero turns           → nothing_needed
//  5. it left an incomplete plan behind     → stopped_early
//  6. otherwise                             → did_work
//
// Steps 1/3/4/5 are pure signal — an error, a pending-gate row, a turn count,
// a plan's step states. No prose, nothing the LLM can forget.
func classifyOutcome(ctx context.Context, pool *pgxpool.Pool, sessionID string, summary RunSummary, execErr error) Outcome {
	if execErr != nil {
		return OutcomeFailed
	}
	// UNIVERSAL false-green guard (the boss's law: "Jarvis MUST ALWAYS see errors
	// he gets — no false greens that were really 401s, 404s"). If this run's
	// session logged ANY hard outbound-HTTP failure (auth / rate-limit / timeout /
	// transport / 5xx — captured at the transport by core/internal/httpx), it is
	// NOT a clean success no matter what the executor stamped below. Checked BEFORE
	// the meta.outcome honour so a deterministic task that swallowed a 401 and
	// declared "nothing_needed" still fails → surfaces → feeds the code-proposal
	// backlog (healing keys off last_run_status LIKE 'error%').
	if httpHardFailureInSession(ctx, pool, sessionID) {
		return OutcomeFailed
	}
	// An executor that knows its own internals (e.g. nightly cognition: changed
	// vs. quiet) can stamp meta.outcome; honour it over the generic derivation.
	if raw, ok := summary.Meta["outcome"].(string); ok {
		if o := Outcome(raw); o.valid() {
			return o
		}
	}
	if pool != nil && sessionID != "" {
		// The run hit a both-bridges-down wall — it physically couldn't run
		// files/shell/git, so its coding work was blocked by infrastructure, NOT
		// done. That needs the boss (bring a bridge back), so it must read
		// "needs you", never "did work". The bridge tools stamp a stable
		// 'bridge_unavailable' marker into the tool result → captured as an
		// observation. See tools.bridgeUnavailableResult.
		if bridgeUnavailableInSession(ctx, pool, sessionID) {
			return OutcomeNeedsYou
		}
		// A queued trust contract for this session is the canonical "the boss
		// must decide" signal. Status 'pending' is what the gate waits on.
		var one int
		if err := pool.QueryRow(ctx, `
			SELECT 1 FROM mem_trust_contracts
			 WHERE action_spec->>'session_id' = $1 AND status = 'pending'
			 LIMIT 1
		`, sessionID).Scan(&one); err == nil {
			return OutcomeNeedsYou
		}
	}
	if t, ok := intFromMeta(summary.Meta, "turns"); ok && t == 0 {
		return OutcomeNothingNeeded
	}
	// Work that visibly broke or was force-closed unfinished must never read
	// "done". abandoned_steps is stamped by the executor's finalize (which
	// settles the plan BEFORE this classifier runs — the plan check below can
	// no longer see the abandonment, so the count is the only honest signal);
	// failures counts errored turns inside the run.
	if n, ok := intFromMeta(summary.Meta, "abandoned_steps"); ok && n > 0 {
		return OutcomeStoppedEarly
	}
	if f, ok := intFromMeta(summary.Meta, "failures"); ok && f > 0 {
		return OutcomeStoppedEarly
	}
	// Backstop for executors that don't finalize their own plans.
	if pool != nil && sessionID != "" {
		if p, err := plan.NewStore(pool).GetActiveBySession(ctx, sessionID); err == nil && p != nil && planIncomplete(p) {
			return OutcomeStoppedEarly
		}
	}
	return OutcomeDidWork
}

// httpHardFailureInSession reports whether the run's session logged any HARD
// outbound-HTTP failure in mem_http_failures (the capture seam in
// core/internal/httpx). This is the universal guard that makes a swallowed
// 401/5xx un-hideable: even if the caller dropped the error and reported success,
// the failure was recorded at the transport, and here it vetoes a green outcome.
func httpHardFailureInSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) bool {
	if pool == nil || sessionID == "" {
		return false
	}
	var one int
	err := pool.QueryRow(ctx, `
		SELECT 1 FROM mem_http_failures
		 WHERE session_id = $1 AND `+hardHTTPFailureWhere+`
		 LIMIT 1
	`, sessionID).Scan(&one)
	return err == nil
}

// httpFailureError builds a human error from the most recent hard HTTP failure in
// a session, so a run the guard marked failed (but whose executor returned no Go
// error) still gets a real last_run_status + ping + cron_failure backlog entry.
func httpFailureError(ctx context.Context, pool *pgxpool.Pool, sessionID string) error {
	if pool == nil || sessionID == "" {
		return nil
	}
	var host string
	var status int
	if err := pool.QueryRow(ctx, `
		SELECT host, status FROM mem_http_failures
		 WHERE session_id = $1 AND `+hardHTTPFailureWhere+`
		 ORDER BY occurred_at DESC LIMIT 1
	`, sessionID).Scan(&host, &status); err != nil {
		return nil
	}
	if status == 0 {
		return fmt.Errorf("an outbound request to %s failed with no response during this run", host)
	}
	return fmt.Errorf("an outbound request to %s returned HTTP %d during this run", host, status)
}

// planIncomplete reports whether the plan did not fully succeed — either a step
// is still unsettled (the agent stopped before finishing) or a step explicitly
// failed (the build broke, the verify failed, etc.). A failed step is NOT
// considered "done" even though it's terminal: a run that ended with a failed
// build step should never classify as OutcomeDidWork.
func planIncomplete(p *plan.Plan) bool {
	for _, st := range p.Steps {
		switch st.Status {
		case plan.StepDone, plan.StepSkipped:
			// settled successfully
		default:
			// pending, in_progress, blocked, OR failed → plan did not fully succeed
			return true
		}
	}
	return false
}

func intFromMeta(meta map[string]any, key string) (int, bool) {
	if meta == nil {
		return 0, false
	}
	switch v := meta[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// surfaceRunOutcome posts a finished run's result to "Surfaced by Jarvis" — the
// boss's inbox for things the agent wants to tell him (dashboard surface
// 'runs'; the 'system' surface is deliberately NOT used because the dashboard
// folds that into the Activity stream, not the inbox). One rolling item per
// cron (ExternalID keyed on the cron id) so a nightly job refreshes its single
// card instead of stacking a new one every fire. Best-effort and nil-safe;
// surfacing never blocks or fails the run.
//
// Connector polls are intentionally skipped — they're background sync, not a
// result the boss asked for, and would clutter the inbox.
func (s *Scheduler) surfaceRunOutcome(ctx context.Context, j Job, summary RunSummary, execErr error, outcome Outcome) {
	if s == nil || s.surface == nil {
		return
	}
	if j.JobKind == JobConnectorPoll {
		return
	}

	name := humanizeCronName(j.Name)
	title := name + " — " + outcomeLabel(outcome)
	floor := outcomeFloor(outcome, execErr)
	narrative := strings.TrimSpace(summary.Summary)

	// The row's one-line "why": the agent's own opening words when it did or
	// stopped work, otherwise the templated floor. The full narrative goes in
	// the body (shown on tap). The reason is NOT repeated into the body — the
	// viewer renders reason (italic) above body, so any overlap reads as the
	// duplicated-text bug the boss caught on the nightly cognition card.
	reason := floor
	if outcome == OutcomeDidWork && narrative != "" {
		reason = firstLine(narrative)
	}
	body := narrative
	if body == "" {
		body = floor
	}

	imp := outcomeImportance(outcome)
	item := &surface.Item{
		Surface:          "runs",
		Kind:             "run_outcome",
		Source:           "cron:" + j.Name,
		ExternalID:       "cron-outcome:" + j.ID,
		Title:            title,
		Body:             body,
		Importance:       &imp,
		ImportanceReason: reason,
		Metadata: map[string]any{
			"cron_id":    j.ID,
			"cron_name":  j.Name,
			"outcome":    string(outcome),
			"session_id": j.RunSessionID,
		},
		Status: surface.StatusOpen,
		// Rolling card: tonight's outcome is NEW information. Dismissing last
		// night's card must not swallow it (see surface.Item.Reopen).
		Reopen: true,
	}
	// Informational outcomes self-clear after 36h so the inbox doesn't fill up
	// with stale "nothing to do" rows; the nightly SweepExpired dismisses them.
	// Failures and pending decisions persist until the boss acts on them.
	if outcome == OutcomeNothingNeeded || outcome == OutcomeDidWork || outcome == OutcomeStoppedEarly {
		exp := time.Now().UTC().Add(36 * time.Hour)
		item.ExpiresAt = &exp
	}
	// A failed run gets a one-tap "run it again" so the boss can retry from the
	// inbox without hunting for the cron. Generic surface action: tapping it
	// runs the intent as an agent turn (POST /api/surface/action).
	if outcome == OutcomeFailed {
		item.Actions = []surface.Action{{
			ID:     "retry",
			Label:  "Run it again now",
			Intent: "Re-run the scheduled job \"" + j.Name + "\" now and tell me what happens.",
			Style:  "primary",
		}}
	}

	if _, err := s.surface.Upsert(ctx, item); err != nil {
		log.Printf("cron surface outcome %s: %v", j.Name, err)
	}
}

// outcomeLabel is the short suffix on the inbox card title.
func outcomeLabel(o Outcome) string {
	switch o {
	case OutcomeFailed:
		return "failed"
	case OutcomeNeedsYou:
		return "needs your okay"
	case OutcomeNothingNeeded:
		return "nothing to do"
	case OutcomeStoppedEarly:
		return "stopped early"
	default:
		return "done"
	}
}

// outcomeFloor is the guaranteed plain-English "what this means" line — the
// deterministic floor that reads human even when the agent narrated nothing
// (CLAUDE.md: "Explain value simply (always)" + "Failures must read human").
func outcomeFloor(o Outcome, execErr error) string {
	switch o {
	case OutcomeFailed:
		if execErr != nil {
			if h := errs.Humanize(execErr); strings.TrimSpace(h.Summary) != "" {
				return h.Summary
			}
		}
		return "I hit a snag and couldn't finish. Nothing's broken; I'll try again on the next run."
	case OutcomeNeedsYou:
		return "I need your okay before I take the next step."
	case OutcomeNothingNeeded:
		return "Everything was already in order — nothing needed your attention."
	case OutcomeStoppedEarly:
		return "I stopped before finishing everything. Open this to see what is left."
	default:
		return ""
	}
}

// outcomeImportance maps an outcome onto the surface importance scale (0-100)
// the dashboard uses for tint + ordering: >=80 reads danger, 50-79 reads info,
// <50 is muted. Failures shout, decisions stand out, FYIs stay quiet.
func outcomeImportance(o Outcome) int {
	switch o {
	case OutcomeFailed:
		return 85
	case OutcomeNeedsYou:
		return 65
	case OutcomeStoppedEarly:
		return 42
	case OutcomeDidWork:
		return 30
	default: // nothing_needed
		return 18
	}
}

// humanizeCronName turns a slug like "nightly-self-improve" into "Nightly self
// improve" for the card title (mirrors dashboard.humanizeName, kept local to
// avoid a cross-package dependency for one helper).
func humanizeCronName(raw string) string {
	s := strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(strings.TrimSpace(raw))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return raw
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
