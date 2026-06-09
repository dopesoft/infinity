package proactive

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Healing checklist - the "Jarvis noticed something broke" detectors.
//
// Two scanners that fire every heartbeat tick:
//
//  1. Failed crons. mem_crons rows whose last_run_status starts with
//     "error" mean a scheduled job blew up since the last successful
//     fire. Surface each one as a curiosity question with
//     source_kind='cron_failure' so it shows up in /lab's Fix this tab
//     with an Approve-and-fix path that hands the failure to a Live
//     session.
//
//  2. Repeated tool failures. mem_observations rows with
//     hook_name='PostToolUseFailure' grouped by tool name. When the
//     same tool fails 3 or more times in the last 24 hours, that's
//     load-bearing - it's eating turns the boss expected to work.
//     Surface one curiosity question per offending tool with
//     source_kind='repeated_tool_error'.
//
// Both detectors are deterministic SQL; no LLM. Dedup is handled at the
// schema level by mem_curiosity_questions's unique index on
// (question) WHERE status='open' - re-running the scan on the next
// tick is a no-op if the question text is identical and still open.

// Cron-failure detection threshold. last_run_status starts with this
// prefix when scheduler.RunOnce or the regular tick records an error.
const cronErrorPrefix = "error"

// Repeated-tool-error threshold. A tool has to fail at least this many
// times in the look-back window before it earns a Fix-this proposal.
const repeatedErrorThreshold = 5
const repeatedErrorWindow = "24 hours"

// localLoc resolves the boss's timezone once so every surfaced timestamp
// reads in HIS frame (CST/CDT by default), never a UTC RFC3339 string he
// can't parse at a glance. Mirrors initiative.NewLocalTimeProvider's default;
// a bad/empty INFINITY_USER_TIMEZONE falls back to UTC rather than crashing.
var localLoc = func() *time.Location {
	tz := strings.TrimSpace(os.Getenv("INFINITY_USER_TIMEZONE"))
	if tz == "" {
		tz = "America/Chicago"
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}()

// humanWhen renders t as a casual local-time phrase ("today at 12:29am",
// "yesterday at 8:34pm", "Mon 1 Jun at 11:35am") instead of a UTC RFC3339
// stamp. Findings are boss-facing — he reads them at a glance — so they
// speak his clock, per the local-time provider's rule. Zero time → "".
func humanWhen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	lt := t.In(localLoc)
	now := time.Now().In(localLoc)
	clock := strings.ToLower(lt.Format("3:04pm"))
	y, m, d := lt.Date()
	ny, nm, nd := now.Date()
	yy, ym, yd := now.AddDate(0, 0, -1).Date()
	switch {
	case y == ny && m == nm && d == nd:
		return "today at " + clock
	case y == yy && m == ym && d == yd:
		return "yesterday at " + clock
	default:
		return lt.Format("Mon 2 Jan") + " at " + clock
	}
}

// MacBridgeProbe is the optional callback the healer uses to find out
// whether the Mac bridge is currently reachable. When the probe is
// non-nil and returns false, scanRepeatedToolErrors skips claude_code__*
// failures and auto-resolves any open questions for them - those
// failures are EXPECTED when the boss is on cloud, and re-emitting
// "fs_read failed N times" curiosity questions every heartbeat tick is
// pure noise. Nil probe disables the guard (same behavior as before
// migration 040).
type MacBridgeProbe func() bool

// HealingChecklist returns a Checklist function that runs both
// scanners and emits Findings for any newly-detected problem. Compose
// with DefaultChecklist + CuriosityChecklist via ComposeChecklists.
//
// macBridgeHealthy is an optional callback used to short-circuit the
// repeated-tool-error scanner when the Mac bridge is offline. Pass
// `nil` to disable the guard (legacy behavior).
func HealingChecklist(pool *pgxpool.Pool, macBridgeHealthy MacBridgeProbe) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		var findings []Finding
		findings = append(findings, scanCronFailures(ctx, pool)...)
		findings = append(findings, scanRepeatedToolErrors(ctx, pool, macBridgeHealthy)...)
		return findings, nil
	}
}

func scanCronFailures(ctx context.Context, pool *pgxpool.Pool) []Finding {
	// First sweep: every cron that is currently passing or disabled
	// gets its prior 'cron_failure:<id>' open question(s) closed. That
	// way the dashboard stops showing "Cron X is failing" after the
	// boss already fixed the routing - even if the heartbeat doesn't
	// have a fresh failure to emit this tick.
	if pool != nil {
		_, _ = pool.Exec(ctx, `
			UPDATE mem_curiosity_questions q
			   SET status = 'dismissed',
			       resolved_at = NOW(),
			       resolved_reason = 'condition_cleared'
			 WHERE q.status = 'open'
			   AND q.source_tag LIKE 'cron_failure:%'
			   AND NOT EXISTS (
			       SELECT 1 FROM mem_crons c
			        WHERE 'cron_failure:' || c.id::text = q.source_tag
			          AND COALESCE(c.enabled, TRUE) = TRUE
			          AND COALESCE(c.last_run_status,'') LIKE 'error%'
			   )
		`)
	}
	// Pull the REAL failure detail from the cron's most recent failed run, not
	// just the one-line humanized title on mem_crons.last_run_status (which is
	// often the generic "Something went wrong and I stopped" fallback). The
	// mem_runs row carries: what the run was doing (result_summary), the raw
	// error, and the structured human_error {summary, action, raw}. We also pull
	// the cron's target prompt so the question explains what the job even does.
	rows, err := pool.Query(ctx, `
		SELECT c.id::text,
		       c.name,
		       COALESCE(c.last_run_status,''),
		       c.last_run_at,
		       COALESCE(c.target,''),
		       COALESCE(r.result_summary,''),
		       COALESCE(r.error,''),
		       COALESCE(r.human_error->>'summary',''),
		       COALESCE(r.human_error->>'action',''),
		       COALESCE(r.human_error->>'raw','')
		  FROM mem_crons c
		  LEFT JOIN LATERAL (
		      SELECT result_summary, error, human_error
		        FROM mem_runs
		       WHERE kind = 'cron' AND target_id = c.id::text AND status = 'error'
		       ORDER BY started_at DESC
		       LIMIT 1
		  ) r ON TRUE
		 WHERE c.last_run_status LIKE $1
		   AND COALESCE(c.enabled, TRUE) = TRUE
		 ORDER BY c.last_run_at DESC NULLS LAST
		 LIMIT 20
	`, cronErrorPrefix+"%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var (
			id, name, status              string
			lastRun                       *time.Time
			target, summary               string
			rawErr, humanSummary          string
			humanAction, humanRaw         string
		)
		if err := rows.Scan(&id, &name, &status, &lastRun, &target,
			&summary, &rawErr, &humanSummary, &humanAction, &humanRaw); err != nil {
			continue
		}
		question := fmt.Sprintf("One of my scheduled tasks (%q) keeps failing. Want me to fix it?", name)
		rationale := buildCronFailureContext(name, status, lastRun, target,
			summary, rawErr, humanSummary, humanAction, humanRaw)
		tag := "cron_failure:" + id
		// Compound merge via UpsertQuestion: same source_tag updates the
		// existing open row in place (occurrences_count++, evidence_log
		// gets the latest sample, rationale refreshes, importance
		// escalates if changed). No more "title varied so a new row
		// appeared" duplication.
		_, _, _, _ = UpsertQuestion(ctx, pool, slog.Default(), QuestionDraft{
			Question:   question,
			Rationale:  rationale,
			SourceKind: "cron_failure",
			SourceTag:  tag,
			SourceIDs:  []string{id},
			Importance: 9,
			Sample:     rationale,
			Expected:   fmt.Sprintf("cron %q passes on next run after intervention", name),
		})
		out = append(out, Finding{
			Kind:      "self_heal",
			Source:    "cron_failure",
			Title:     question,
			Detail:    rationale,
			SourceTag: tag,
		})
	}
	return out
}

// buildCronFailureContext composes the boss-facing "what actually broke" for a
// failing cron. It layers everything we know — when it ran, what the job does,
// what the run was doing before it died, the real error (not the generic
// humanized title), and the suggested fix — so the curiosity card reads as a
// diagnosis, not a shrug. Lines with no content are skipped so it never pads.
func buildCronFailureContext(name, status string, lastRun *time.Time, target, summary, rawErr, humanSummary, humanAction, humanRaw string) string {
	var b strings.Builder
	if lastRun != nil {
		fmt.Fprintf(&b, "Last fired %s.\n", humanWhen(*lastRun))
	}
	if t := oneLineCtx(target); t != "" {
		fmt.Fprintf(&b, "What this job does: %s\n", truncate(t, 240))
	}
	if s := oneLineCtx(summary); s != "" {
		fmt.Fprintf(&b, "What it was doing: %s\n", truncate(s, 400))
	}
	// The real failure. Prefer the structured human summary, fall back to the
	// humanized status title; then ALWAYS attach the raw error detail so the
	// boss sees the literal failure, not just a friendly paraphrase.
	why := strings.TrimSpace(humanSummary)
	if why == "" {
		why = strings.TrimSpace(strings.TrimPrefix(status, "error: "))
		why = strings.TrimSpace(strings.TrimPrefix(why, "error (manual): "))
	}
	if why != "" {
		fmt.Fprintf(&b, "Why it failed: %s\n", truncate(why, 300))
	}
	detail := strings.TrimSpace(humanRaw)
	if detail == "" {
		detail = strings.TrimSpace(rawErr)
	}
	if detail != "" && detail != why {
		fmt.Fprintf(&b, "Error detail: %s\n", truncate(oneLineCtx(detail), 500))
	}
	if a := strings.TrimSpace(humanAction); a != "" {
		fmt.Fprintf(&b, "Suggested fix: %s\n", truncate(a, 240))
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		// Nothing landed (no run row yet) — degrade to the old one-liner so the
		// card still says something.
		out = truncate(status, 600)
	}
	return out
}

// oneLineCtx collapses whitespace/newlines so a multi-line error or prompt reads
// as a single line in the curiosity card.
func oneLineCtx(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func scanRepeatedToolErrors(ctx context.Context, pool *pgxpool.Pool, macBridgeHealthy MacBridgeProbe) []Finding {
	// Bridge-offline guard: when the Mac bridge is down, every
	// claude_code__* tool fails. Those failures don't represent the
	// agent doing something wrong - they represent the boss being on
	// cloud while the Mac is asleep. Emitting "fs_read failed N times"
	// curiosity questions every tick is just spam. So when the probe
	// reports false, (a) we resolve any open repeated_tool_error rows
	// for claude_code__* tools with reason 'bridge_offline', and (b) we
	// filter those failures out of the freshness query below so no
	// new rows are inserted for them.
	macUp := macBridgeHealthy == nil || macBridgeHealthy()
	// claude_code__* command-level failures are NEVER a self-heal signal:
	// a grep that exits 1, a Read on a missing path, or a failing build is
	// normal agent work, not a broken tool. We retire any open
	// repeated_tool_error:claude_code__* rows unconditionally (the reason
	// distinguishes "Mac asleep" from "just normal coding noise") and the
	// freshness query below excludes the class via EligibleForRepeatedError,
	// so they never regenerate. This killed the 45×/45× claude_code__Bash /
	// __Read spam. See tool_policy.go.
	if pool != nil {
		reason := "coding_bridge_noise"
		if !macUp {
			reason = "bridge_offline"
		}
		_, _ = pool.Exec(ctx, `
			UPDATE mem_curiosity_questions
			   SET status = 'dismissed',
			       resolved_at = NOW(),
			       resolved_reason = $1,
			       auto_dismissed_at = NOW()
			 WHERE status = 'open'
			   AND source_tag LIKE 'repeated_tool_error:claude_code__%'
		`, reason)
	}

	// First sweep: any tool whose old 'repeated_tool_error:<tool>' open
	// question exists but has zero recent failures in the window is
	// cleared. Keeps the dashboard honest when a tool stabilizes.
	if pool != nil {
		_, _ = pool.Exec(ctx, `
			UPDATE mem_curiosity_questions q
			   SET status = 'dismissed',
			       resolved_at = NOW(),
			       resolved_reason = 'condition_cleared'
			 WHERE q.status = 'open'
			   AND q.source_tag LIKE 'repeated_tool_error:%'
			   AND NOT EXISTS (
			       SELECT 1 FROM mem_observations o
			        WHERE o.hook_name = 'PostToolUseFailure'
			          AND o.created_at > NOW() - INTERVAL '`+repeatedErrorWindow+`'
			          AND 'repeated_tool_error:' || COALESCE(o.payload->>'name','') = q.source_tag
			   )
		`)
	}

	// Recovered sweep: a tool that has SUCCEEDED more recently than its last
	// failure is fixed - clear its open card NOW even though stale failures
	// still sit in the window. Without this, fixing a tool (or deploying a fix)
	// still shows "Tool X keeps failing" for a full 24h, which is precisely
	// what makes a healthy system feel broken. Clears both the curiosity
	// question and the self_heal finding (the Activity-stream row) so the two
	// surfaces stay in lockstep.
	if pool != nil {
		const recoveredCond = `
			   AND EXISTS (
			       SELECT 1 FROM mem_observations o
			        WHERE o.created_at > NOW() - INTERVAL '` + repeatedErrorWindow + `'
			          AND 'repeated_tool_error:' || COALESCE(o.payload->>'name','') = t.source_tag
			       HAVING MAX(o.created_at) FILTER (WHERE o.hook_name = 'PostToolUse')
			            > MAX(o.created_at) FILTER (WHERE o.hook_name = 'PostToolUseFailure')
			   )`
		_, _ = pool.Exec(ctx, `
			UPDATE mem_curiosity_questions t
			   SET status = 'dismissed', resolved_at = NOW(), resolved_reason = 'recovered'
			 WHERE t.status = 'open'
			   AND t.source_tag LIKE 'repeated_tool_error:%'`+recoveredCond)
		_, _ = pool.Exec(ctx, `
			UPDATE mem_heartbeat_findings t
			   SET status = 'resolved', resolved_at = NOW()
			 WHERE t.status = 'open'
			   AND t.source_tag LIKE 'repeated_tool_error:%'`+recoveredCond)
	}

	// Build the freshness query. claude_code__* (coding-bridge class) is
	// excluded unconditionally - command-level non-zero exits are normal
	// coding work, never a tool malfunction. Excluding at the query level
	// also stops claude_code failures from consuming the LIMIT 10 and
	// crowding out genuinely-broken tools. The Go-side EligibleForRepeatedError
	// guard below keeps tool_policy.go the single source of truth.
	toolExclusion := "AND COALESCE(payload->>'name','') NOT LIKE 'claude_code__%'"
	// Group PostToolUseFailure observations by tool name (extracted from
	// the JSON payload). A tool that fails THRESHOLD+ times in WINDOW
	// gets one curiosity question; the sample error is the most recent
	// occurrence so the rationale carries something actionable. When
	// the Mac bridge is offline, claude_code__* failures are excluded
	// at the query level via toolExclusion.
	rows, err := pool.Query(ctx, `
		WITH last_success AS (
			-- Most recent SUCCESSFUL call per tool in the window. A tool that
			-- has succeeded since its last failure is fixed - we must not keep
			-- crying "keeps failing" off stale failures that already recovered.
			SELECT payload->>'name' AS tool_name, MAX(created_at) AS ok_at
			  FROM mem_observations
			 WHERE hook_name = 'PostToolUse'
			   AND created_at > NOW() - INTERVAL '`+repeatedErrorWindow+`'
			   AND COALESCE(payload->>'name','') <> ''
			 GROUP BY payload->>'name'
		),
		failures AS (
			SELECT
				o.payload->>'name' AS tool_name,
				o.payload->>'output' AS sample_output,
				o.created_at
			  FROM mem_observations o
			  LEFT JOIN last_success s ON s.tool_name = o.payload->>'name'
			 WHERE o.hook_name = 'PostToolUseFailure'
			   AND o.created_at > NOW() - INTERVAL '`+repeatedErrorWindow+`'
			   AND COALESCE(o.payload->>'name','') <> ''
			   -- THE fix for already-resolved issues resurfacing: only count a
			   -- failure the tool has NOT recovered from. A success AFTER the
			   -- failure (a fix, a deploy) clears it from the signal, so a tool
			   -- the boss fixed at 5:30am stops showing "keeps failing" instantly
			   -- instead of haunting the board for a full 24h window.
			   AND (s.ok_at IS NULL OR o.created_at > s.ok_at)
			   `+toolExclusion+`
		),
		grouped AS (
			SELECT
				tool_name,
				COUNT(*) AS hits,
				MAX(created_at) AS last_seen,
				(ARRAY_AGG(sample_output ORDER BY created_at DESC))[1] AS sample
			  FROM failures
			 GROUP BY tool_name
		)
		SELECT tool_name, hits, last_seen, COALESCE(sample,'')
		  FROM grouped
		 WHERE hits >= $1
		 ORDER BY hits DESC, last_seen DESC
		 LIMIT 10
	`, repeatedErrorThreshold)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var (
			tool, sample string
			hits         int
			lastSeen     time.Time
		)
		if err := rows.Scan(&tool, &hits, &lastSeen, &sample); err != nil {
			continue
		}
		// Policy guard (single source of truth): coding-bridge tools never
		// surface a "fix this tool" self-heal question. Redundant with the SQL
		// exclusion above, but keeps the decision in tool_policy.go.
		if !EligibleForRepeatedError(tool) {
			continue
		}
		// Canonical question phrasing - stays constant across merges.
		// Detail/occurrences_count carry the latest count via the
		// evidence_log, so we no longer have to bake "N times" into the
		// title (which would create count-varying duplicates).
		question := fmt.Sprintf("One of the tools I use (%q) keeps erroring out. Want me to fix it?", tool)
		rationale := fmt.Sprintf("Most recent failure %s\n\n%s",
			humanWhen(lastSeen), truncate(sample, 600))
		tag := "repeated_tool_error:" + tool
		_, _, _, _ = UpsertQuestion(ctx, pool, slog.Default(), QuestionDraft{
			Question:   question,
			Rationale:  rationale,
			SourceKind: "repeated_tool_error",
			SourceTag:  tag,
			Importance: 8,
			Sample:     fmt.Sprintf("hits=%d last=%s sample=%s", hits, lastSeen.UTC().Format(time.RFC3339), truncate(sample, 200)),
			// Pre prediction: when the boss approves the fix-this
			// affordance, the agent's intervention should clear this
			// specific failure mode within ~5 turns. Post-resolution
			// surprise scoring (when the agent reports its outcome)
			// feeds Voyager curriculum if the fix didn't actually work.
			Expected: fmt.Sprintf("tool=%q failures drop to zero within 5 turns after intervention", tool),
		})
		out = append(out, Finding{
			Kind:      "self_heal",
			Source:    "repeated_tool_error",
			Title:     question,
			Detail:    rationale,
			SourceTag: tag,
		})
	}
	return out
}

// insertHealingQuestion writes (or no-ops on conflict) a curiosity
// question with the given source_kind. Dedupe rides on the unique
// index on (question) WHERE status='open' so re-runs of the scan are
// idempotent across heartbeat ticks. Returns true when a NEW row
// landed so the caller can decide whether to emit a Finding.
//
// source_kind doubles as the source_tag prefix; callers that want
// per-condition dedup (e.g. cron_failure for cron id X vs cron id Y)
// should use insertHealingQuestionWithTag below instead.
func insertHealingQuestion(
	ctx context.Context,
	pool *pgxpool.Pool,
	question, rationale, sourceKind string,
	sourceIDs []string,
	importance int,
) bool {
	return insertHealingQuestionWithTag(ctx, pool, question, rationale, sourceKind, sourceKind, sourceIDs, importance)
}

// insertHealingQuestionWithTag is the load-bearing form. source_tag is
// the stable identifier for "what condition this is about", e.g.
// "cron_failure:<id>" or "repeated_tool_error:<tool>". When a later
// tick's question for the same tag has different text (count varies,
// error message changes), the schema lifecycle (migration 036) lets
// ResolveQuestionsBySourceTag close the older row so the dashboard
// stops piling up stale questions.
func insertHealingQuestionWithTag(
	ctx context.Context,
	pool *pgxpool.Pool,
	question, rationale, sourceKind, sourceTag string,
	sourceIDs []string,
	importance int,
) bool {
	if pool == nil {
		return false
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	// Route through UpsertQuestion - the canonical path - so every detector
	// inherits the cooldown precheck (a dismissed tag stays silent for 24h)
	// and the pattern-suppression check (a pattern_key the boss dismissed 3+
	// times is suppressed durably via procedural memory). The old raw INSERT
	// bypassed both, which is how a single dismissed "Crystallize X?" question
	// regenerated hundreds of times. Returns true only on a fresh insert, so
	// callers still emit one Finding per genuinely-new condition.
	_, isNew, _, err := UpsertQuestion(ctx, pool, slog.Default(), QuestionDraft{
		Question:   question,
		Rationale:  rationale,
		SourceKind: sourceKind,
		SourceTag:  sourceTag,
		SourceIDs:  sourceIDs,
		Importance: importance,
		Sample:     question,
	})
	if err != nil {
		return false
	}
	return isNew
}

// ResolveQuestionsBySourceTag marks every open curiosity question with
// the given source_tag as dismissed with reason 'condition_cleared'.
// Mirror of ResolveSourceTag for findings - lets a scanner explicitly
// close questions when the underlying condition is no longer present
// (e.g. the cron that was failing is now passing). No-op for empty tag
// or nil pool.
func ResolveQuestionsBySourceTag(ctx context.Context, pool *pgxpool.Pool, tag string) {
	if pool == nil || tag == "" {
		return
	}
	_, _ = pool.Exec(ctx, `
		UPDATE mem_curiosity_questions
		   SET status = 'dismissed',
		       resolved_at = NOW(),
		       resolved_reason = 'condition_cleared'
		 WHERE source_tag = $1 AND status = 'open'
	`, tag)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
