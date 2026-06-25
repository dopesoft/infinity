package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	robfig "github.com/robfig/cron/v3"
)

// infoLog writes success/operational lines to stdout so Railway tags them
// `severity:info` instead of the red `error` that stdlib log (stderr) would
// produce. Failures still use the default stderr logger. See CLAUDE.md →
// "Logging — severity must match reality".
var infoLog = log.New(os.Stdout, "cron: ", log.LstdFlags)

// Scheduler wraps robfig/cron/v3 with DB-backed job state.
type Scheduler struct {
	pool     *pgxpool.Pool
	executor Executor
	surface  *surface.Store // posts each run's outcome to "Surfaced by Jarvis"
	cron     *robfig.Cron
	parser   robfig.Parser
	loc      *time.Location

	mu      sync.Mutex
	entries map[string]robfig.EntryID // job ID → robfig entry
}

// resolveLocation picks the timezone the scheduler interprets cron
// expressions in. ONE convention across the whole product: the boss's local
// frame, NOT UTC. We read the SAME env the LocalTimeProvider uses
// (INFINITY_USER_TIMEZONE, default America/Chicago) so "9am" in a cron means
// 9am on the boss's wall clock — and it stays 9am across DST because the
// location, not a fixed offset, drives the schedule. A bad IANA name falls
// back to UTC rather than crashing boot.
//
// Before this, robfig was hardcoded to time.UTC, so "0 8 * * *" fired at 8am
// UTC (≈2-3am Central, drifting with DST) while Studio rendered every
// timestamp in browser-local time — the exact "some say UTC, some say
// Central" inconsistency the boss called out. Anchoring the engine to the
// boss's zone is what makes a single displayed convention honest.
func resolveLocation() *time.Location {
	tz := strings.TrimSpace(os.Getenv("INFINITY_USER_TIMEZONE"))
	if tz == "" {
		tz = "America/Chicago"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func New(pool *pgxpool.Pool, exec Executor) *Scheduler {
	parser := robfig.NewParser(
		robfig.Minute | robfig.Hour | robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor,
	)
	loc := resolveLocation()
	c := robfig.New(robfig.WithParser(parser), robfig.WithLocation(loc))
	return &Scheduler{
		pool:     pool,
		executor: exec,
		surface:  surface.NewStore(pool, slog.Default()),
		cron:     c,
		parser:   parser,
		loc:      loc,
		entries:  make(map[string]robfig.EntryID),
	}
}

// Location returns the timezone the scheduler interprets schedules in, so
// callers (and the HTTP layer) can label times for the boss in the same
// frame the engine fires them.
func (s *Scheduler) Location() *time.Location {
	if s == nil || s.loc == nil {
		return time.UTC
	}
	return s.loc
}

// Start kicks off the underlying cron and loads every enabled row.
func (s *Scheduler) Start(ctx context.Context) error {
	s.cron.Start()
	return s.Reload(ctx)
}

// Stop halts the scheduler. It does NOT cancel running jobs.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Reload synchronises the in-memory schedule with the database.
func (s *Scheduler) Reload(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, schedule, COALESCE(schedule_natural,''), job_kind, target,
		       COALESCE(target_config::text, '{}'),
		       enabled, max_retries, backoff_seconds,
		       last_run_at, COALESCE(last_run_status,''), COALESCE(last_run_duration_ms,0),
		       next_run_at, failure_count, created_at
		  FROM mem_crons
		 WHERE enabled = TRUE
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	wanted := make(map[string]Job)
	for rows.Next() {
		var j Job
		var kind string
		var targetCfg string
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.ScheduleNatural, &kind,
			&j.Target, &targetCfg, &j.Enabled, &j.MaxRetries, &j.BackoffSeconds,
			&j.LastRunAt, &j.LastRunStatus, &j.LastRunDuration,
			&j.NextRunAt, &j.FailureCount, &j.CreatedAt); err != nil {
			return err
		}
		j.JobKind = JobKind(kind)
		j.TargetConfig = []byte(targetCfg)
		wanted[j.ID] = j
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove jobs no longer in the desired set.
	for id, entry := range s.entries {
		if _, ok := wanted[id]; !ok {
			s.cron.Remove(entry)
			delete(s.entries, id)
		}
	}

	// Add new + update existing.
	for id, j := range wanted {
		if existing, ok := s.entries[id]; ok {
			s.cron.Remove(existing)
		}
		entryID, err := s.cron.AddFunc(j.Schedule, s.makeFireFn(j))
		if err != nil {
			fmt.Printf("cron: skipping %q (bad schedule %q): %v\n", j.Name, j.Schedule, err)
			continue
		}
		s.entries[id] = entryID
		// Persist the next fire so the dashboard "Queued" column (which filters
		// enabled crons on next_run_at > now) actually shows upcoming runs.
		// Without this, next_run_at stays NULL forever and nothing is queued.
		if next, nerr := s.nextFire(j.Schedule); nerr == nil {
			_, _ = s.pool.Exec(ctx, `UPDATE mem_crons SET next_run_at = $2 WHERE id = $1::uuid`, id, next)
		}
	}
	return nil
}

// jobTimeout bounds a single scheduled fire. Deterministic kinds
// (system_task, connector_poll) finish fast and keep the original 5-min cap.
// Agent-driven kinds (isolated_agent_turn, system_event) can legitimately run
// long — the nightly self-improve session edits, builds, tests, commits, and
// pushes — so they get a much larger budget (override via
// INFINITY_CRON_AGENT_TIMEOUT). Without this, the old hardcoded 5-min ctx
// cancelled real agent work mid-build.
func jobTimeout(j Job) time.Duration {
	switch j.JobKind {
	case JobIsolatedAgentTurn, JobSystemEvent:
		if v := strings.TrimSpace(os.Getenv("INFINITY_CRON_AGENT_TIMEOUT")); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
		return 30 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func (s *Scheduler) makeFireFn(j Job) func() {
	return func() {
		start := time.Now().UTC()
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout(j))
		defer cancel()

		// Overlap guard: if a prior run of THIS cron is still in-flight, skip
		// this fire instead of stacking a second concurrent run. A stalled or
		// slow run otherwise piles up a fresh zombie session/plan every period —
		// the exact inbox-triage double-fire the boss saw. Keyed on the cron's
		// own mem_runs rows (idx_mem_runs_target_running); generic for every
		// cron. Manual RunOnce is deliberately NOT guarded — that's an explicit
		// boss action and may override.
		if s.cronInFlight(ctx, j) {
			infoLog.Printf("skipping fire of %q (%s) — prior run still in-flight", j.Name, j.ID)
			_, _ = s.pool.Exec(ctx, `
				UPDATE mem_crons SET last_run_at = $2, last_run_status = 'skipped (overlap)'
				 WHERE id = $1::uuid
			`, j.ID, time.Now().UTC())
			return
		}

		// Begin/Finish (not Track) so the executor's RunSummary lands on the
		// mem_runs row: result_summary gets the "what I did / how it went"
		// narrative and meta gets the structured detail. That's what turns the
		// kanban "Done" card and the /logs run detail from a bare "ok" into an
		// actual report. The row also drives the live spinner that persists
		// across navigation / focus / refresh. Errors still propagate to the
		// post-run mem_crons UPDATE below unchanged.
		// Mint the run's session id up front and stamp it on the row at BEGIN
		// (not finish). A plan-producing executor uses this same id for its
		// mem_plans row, so while the job runs the board can fold run + plan into
		// one live step-timeline card instead of showing a bare step-less card
		// until done. The executor's own meta (with the real session id, if it
		// differs) still lands at finish below — the jsonb merge lets the later
		// write win for any overlapping key.
		j.RunSessionID = uuid.NewString()
		// The session id must exist in mem_sessions BEFORE any executor writes a
		// mem_plans row keyed on it — mem_plans.session_id is a FK to
		// mem_sessions, so a bare minted uuid makes plan.Create fail (SQLSTATE
		// 23503) and the step-timeline card silently never appears. Book the row
		// here, once, for every fire.
		s.ensureSession(ctx, j.RunSessionID)
		handle := runs.BeginGlobal(ctx, runs.KindCron, j.ID, j.Name, runs.SourceScheduled)
		handle.SetMeta(ctx, map[string]any{"session_id": j.RunSessionID})
		summary, execErr, attempts := s.executeWithRetries(ctx, j)

		// Finalize on a FRESH context — never the (possibly expired/cancelled)
		// job ctx. A run that hit its 30-min budget, or whose container is
		// mid-SIGTERM from a self-push redeploy, must still classify + close +
		// surface correctly. Reusing the spent job ctx here silently errored
		// every classifyOutcome guard query and fell through to a false did_work
		// green — the "nightly stall" with no reason. Precedent: the agent
		// executor's finalizeSession already finalizes on context.Background().
		fctx, fcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer fcancel()

		// Classify the run into a single boss-facing outcome (failed /
		// needs_you / nothing_needed / stopped_early / did_work) and apply the
		// deterministic post-classification adjustments. Shared with the reaper
		// reconciler so a redeploy-killed run gets the identical treatment.
		outcome, summary, execErr := s.finalizeOutcome(fctx, j, summary, execErr)

		meta := runMetaWithAttempts(summary.Meta, attempts)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["outcome"] = string(outcome)
		handle.SetMeta(fctx, meta)
		handle.Finish(fctx, execErr, summary.Summary)
		s.surfaceRunOutcome(fctx, j, summary, execErr, outcome)

		s.recordCronRun(fctx, j, execErr, time.Now().UTC().Sub(start).Milliseconds())
	}
}

// finalizeOutcome classifies a finished run into a single boss-facing Outcome
// and applies the two deterministic post-classification adjustments: synthesize
// an http error when the universal guard failed a run whose executor swallowed
// the 401 (so the status line, ping, and cron_failure backlog all engage), and
// replace the narrative when both bridges were down (so no misleading "healthy"
// wording survives). All read queries run on the PASSED ctx — callers must hand
// it a FRESH ctx so an expired job budget can't skip the guards (the stall bug).
// Shared by makeFireFn and the reaper's ReconcileReaped.
func (s *Scheduler) finalizeOutcome(ctx context.Context, j Job, summary RunSummary, execErr error) (Outcome, RunSummary, error) {
	outcome := classifyOutcome(ctx, s.pool, j.RunSessionID, summary, execErr)
	if outcome == OutcomeFailed && execErr == nil {
		if he := httpFailureError(ctx, s.pool, j.RunSessionID); he != nil {
			execErr = he
		}
	}
	if outcome == OutcomeNeedsYou && bridgeUnavailableInSession(ctx, s.pool, j.RunSessionID) {
		summary.Summary = bridgeBlockedNarrative
	}
	return outcome, summary, execErr
}

// recordCronRun stamps the cron row's last-run bookkeeping (plain-language
// status line, duration, failure count, next fire). Kept as one helper so both
// the live fire path and the reaper reconciler write an identical row — the
// dashboard cron list and the cron_failure backlog feeder (which keys off
// last_run_status LIKE 'error%') stay consistent however the run finished.
func (s *Scheduler) recordCronRun(ctx context.Context, j Job, execErr error, durationMS int64) {
	status := "ok"
	if execErr != nil {
		// Store the plain-language title, not the raw provider string, so the
		// cron list reads "Your model token needs reconnecting" instead of
		// "error: openai_oauth: status=401 body={...}". The full run detail
		// (raw + human) still lives on the mem_runs row.
		status = "error: " + errs.Humanize(execErr).Title
	}
	// Advance next_run_at to the upcoming fire so the dashboard "Queued" column
	// rolls forward instead of showing a stale/elapsed time.
	var nextPtr *time.Time
	if next, nerr := s.nextFire(j.Schedule); nerr == nil {
		nextPtr = &next
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE mem_crons SET
		  last_run_at = $2,
		  last_run_status = $3,
		  last_run_duration_ms = $4,
		  failure_count = CASE WHEN $5 THEN failure_count + 1 ELSE 0 END,
		  next_run_at = COALESCE($6, next_run_at)
		 WHERE id = $1::uuid
	`, j.ID, time.Now().UTC(), status, durationMS, execErr != nil, nextPtr)
}

func (s *Scheduler) executeWithRetries(ctx context.Context, j Job) (RunSummary, error, int) {
	attempts := 0
	maxAttempts := 1
	if j.MaxRetries > 0 {
		maxAttempts += j.MaxRetries
	}
	backoff := time.Duration(j.BackoffSeconds) * time.Second
	var lastSummary RunSummary
	var lastErr error
	for attempts < maxAttempts {
		attempts++
		if s.executor != nil {
			lastSummary, lastErr = s.executor.ExecuteJob(j)
		} else {
			lastSummary, lastErr = RunSummary{}, errors.New("no executor configured")
		}
		if lastErr == nil || attempts >= maxAttempts {
			return lastSummary, lastErr, attempts
		}
		infoLog.Printf("retrying cron %q after attempt %d/%d failed: %v", j.Name, attempts, maxAttempts, lastErr)
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return lastSummary, ctx.Err(), attempts
			case <-time.After(backoff):
			}
		}
	}
	return lastSummary, lastErr, attempts
}

func runMetaWithAttempts(meta map[string]any, attempts int) map[string]any {
	if attempts <= 1 {
		return meta
	}
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out["attempts"] = attempts
	return out
}

// ensureSession books a mem_sessions row for the run's minted session id so any
// executor that writes a session-keyed artifact (mem_plans, mem_turns, …) doesn't
// trip a foreign-key violation. mem_plans.session_id REFERENCES mem_sessions(id),
// so without this the inbox-triage step plan (and any future plan-producing
// system task) fails to insert and the live step-timeline card never shows.
// Best-effort + idempotent (ON CONFLICT DO NOTHING); a failure here never blocks
// the fire — the executor just falls back to a NULL/own session as before.
func (s *Scheduler) ensureSession(ctx context.Context, id string) {
	if s == nil || s.pool == nil || id == "" {
		return
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO mem_sessions (id, kind, started_at)
		VALUES ($1::uuid, 'cron', NOW())
		ON CONFLICT (id) DO NOTHING
	`, id); err != nil {
		log.Printf("cron: ensureSession %s: %v", id, err)
	}
}

// cronInFlight reports whether a run of this cron is still executing, so the
// fire path can skip an overlapping concurrent run of the same schedule. Fails
// OPEN (returns false) on any query error — a rare overlap is better than a
// cron that silently never fires again because one status check hiccupped.
func (s *Scheduler) cronInFlight(ctx context.Context, j Job) bool {
	if s == nil || s.pool == nil {
		return false
	}
	// Only a run YOUNGER than the job's own budget + grace counts as genuinely
	// in-flight. A row older than that is a zombie — its container died mid-run,
	// or finalize never closed it — and treating it as "running" would wedge the
	// cron so it never fires again until the reaper clears it (the exact
	// "stalled and never ran again" failure). Bounding the window can never clip
	// a legitimately long run (the lower bound is longer than the job's own
	// timeout) and strictly prevents a permanent wedge.
	staleCutoff := (jobTimeout(j) + 5*time.Minute).Seconds()
	var inFlight bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM mem_runs
			 WHERE kind = $1 AND target_id = $2 AND status = 'running'
			   AND started_at > NOW() - ($3 * interval '1 second')
		)
	`, string(runs.KindCron), j.ID, staleCutoff).Scan(&inFlight)
	if err != nil {
		return false
	}
	return inFlight
}

// Upsert creates or updates a cron row by name. Returns the row's UUID.
func (s *Scheduler) Upsert(ctx context.Context, j Job) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("scheduler unconfigured")
	}
	if !j.JobKind.Valid() {
		return "", fmt.Errorf("invalid job_kind %q", j.JobKind)
	}
	if _, err := s.parser.Parse(j.Schedule); err != nil {
		return "", fmt.Errorf("invalid cron schedule: %w", err)
	}
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	targetCfg := string(j.TargetConfig)
	if targetCfg == "" {
		targetCfg = "{}"
	}
	// Auto-derive the cron's tool scope from the skill it runs, so a scoped
	// skill (e.g. inbox-triage) locks every cron that invokes it WITHOUT anyone
	// hand-setting target_config. The boss makes crons by asking the agent; the
	// lockdown must follow the skill deterministically, not depend on the agent
	// remembering to attach a scope (Rule #1b).
	targetCfg = s.deriveToolScope(ctx, j.Target, targetCfg)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_crons (id, name, schedule, schedule_natural, job_kind, target,
		                       target_config, enabled, max_retries, backoff_seconds)
		VALUES ($1::uuid, $2, $3, NULLIF($4,''), $5, $6, $7::jsonb, $8, $9, $10)
		ON CONFLICT (name) DO UPDATE SET
		  schedule = EXCLUDED.schedule,
		  schedule_natural = EXCLUDED.schedule_natural,
		  job_kind = EXCLUDED.job_kind,
		  target = EXCLUDED.target,
		  target_config = EXCLUDED.target_config,
		  enabled = EXCLUDED.enabled
	`, j.ID, j.Name, j.Schedule, j.ScheduleNatural, string(j.JobKind), j.Target, targetCfg,
		j.Enabled, j.MaxRetries, j.BackoffSeconds)
	if err != nil {
		return "", err
	}
	return j.ID, s.Reload(ctx)
}

// skillInvokeRe extracts the skill name from a cron target that runs a skill,
// e.g. skills_invoke({name:"inbox-triage"}) — tolerant of spaces and quote
// style. Used to auto-derive a cron's tool scope from the skill it runs.
var skillInvokeRe = regexp.MustCompile(`skills_invoke\s*\(\s*\{[^}]*?name\s*:\s*["']([^"']+)["']`)

// deriveToolScope auto-stamps a cron's tool_scope from the skill it invokes, so
// a scoped skill locks every cron that runs it without anyone hand-setting
// target_config. An explicit tool_scope already on the cron wins (the caller
// meant it); a skill with no declared allowed_tools yields no scope (full
// toolset — self-improve / nightly-cognition are unaffected). Returns the
// possibly-updated target_config JSON. Best-effort: any parse/query hiccup
// returns the config unchanged so cron creation never fails over scoping.
func (s *Scheduler) deriveToolScope(ctx context.Context, target, cfg string) string {
	if s == nil || s.pool == nil {
		return cfg
	}
	if strings.TrimSpace(cfg) == "" {
		cfg = "{}"
	}
	merged := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(cfg), &merged); err != nil {
		return cfg
	}
	if _, explicit := merged["tool_scope"]; explicit {
		return cfg // caller set a scope deliberately — respect it
	}
	m := skillInvokeRe.FindStringSubmatch(target)
	if len(m) < 2 {
		return cfg // not a skill-running cron → no scope to derive
	}
	var allowedJSON []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT allowed_tools FROM mem_skills
		 WHERE name = $1 AND allowed_tools IS NOT NULL
		   AND jsonb_typeof(allowed_tools) = 'array'
		   AND jsonb_array_length(allowed_tools) > 0
	`, m[1]).Scan(&allowedJSON); err != nil || len(allowedJSON) == 0 {
		return cfg // skill declares no tool surface → full toolset
	}
	scope, err := json.Marshal(map[string]any{
		"allow":              json.RawMessage(allowedJSON),
		"derived_from_skill": m[1],
	})
	if err != nil {
		return cfg
	}
	merged["tool_scope"] = scope
	out, err := json.Marshal(merged)
	if err != nil {
		return cfg
	}
	infoLog.Printf("derived tool_scope for cron running skill %q from its declared allowed_tools", m[1])
	return string(out)
}

// Delete removes a cron row by id.
func (s *Scheduler) Delete(ctx context.Context, id string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM mem_crons WHERE id = $1::uuid`, id); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// List returns every cron in the database, regardless of enabled state.
func (s *Scheduler) List(ctx context.Context) ([]Job, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, schedule, COALESCE(schedule_natural,''), job_kind, target,
		       COALESCE(target_config::text, '{}'),
		       enabled, max_retries, backoff_seconds,
		       last_run_at, COALESCE(last_run_status,''), COALESCE(last_run_duration_ms,0),
		       next_run_at, failure_count, created_at
		  FROM mem_crons
		 ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var kind string
		var targetCfg string
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.ScheduleNatural, &kind,
			&j.Target, &targetCfg, &j.Enabled, &j.MaxRetries, &j.BackoffSeconds,
			&j.LastRunAt, &j.LastRunStatus, &j.LastRunDuration,
			&j.NextRunAt, &j.FailureCount, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.JobKind = JobKind(kind)
		j.TargetConfig = []byte(targetCfg)
		out = append(out, j)
	}
	return out, rows.Err()
}

// RunOnce fires a single job immediately, regardless of schedule. It does
// not mutate the schedule entry - the next regular firing still happens at
// the cron-expression's next tick. Used by the cron_run_now agent tool and
// by Studio's "Run now" button.
//
// Errors from the executor propagate; the scheduler still updates
// last_run_at / last_run_status so the run shows up in audit.
func (s *Scheduler) RunOnce(ctx context.Context, j Job) error {
	if s == nil {
		return errors.New("scheduler nil")
	}
	start := time.Now().UTC()
	// Begin/Finish exposes "this cron is running" to every device on the
	// network via mem_runs + realtime (survives navigating away from /cron or
	// closing the tab) AND persists the executor's narrative + meta onto the
	// row so the manual run reads as a report, not a bare ok. See CLAUDE.md →
	// "Server-tracked progress".
	// Same as the scheduled path: mint + stamp the session id at begin so a
	// manual "Run now" of a plan-producing job shows its live step timeline.
	j.RunSessionID = uuid.NewString()
	s.ensureSession(ctx, j.RunSessionID) // FK: mem_plans.session_id → mem_sessions (see makeFireFn)
	handle := runs.BeginGlobal(ctx, runs.KindCron, j.ID, j.Name, runs.SourceManual)
	handle.SetMeta(ctx, map[string]any{"session_id": j.RunSessionID})
	summary, execErr, attempts := s.executeWithRetries(ctx, j)
	handle.SetMeta(ctx, runMetaWithAttempts(summary.Meta, attempts))
	handle.Finish(ctx, execErr, summary.Summary)
	end := time.Now().UTC()
	status := "ok (manual)"
	if execErr != nil {
		// Humanize for the cron list's last_run_status, same as the scheduled
		// path (makeFireFn) - so a manual run reads "The model provider hit a
		// snag" instead of raw provider JSON. The full raw error + human_error
		// still land on the mem_runs row via handle.Finish above.
		status = "error (manual): " + errs.Humanize(execErr).Title
	}
	if s.pool != nil {
		_, _ = s.pool.Exec(ctx, `
			UPDATE mem_crons SET
			  last_run_at = $2,
			  last_run_status = $3,
			  last_run_duration_ms = $4
			 WHERE id = $1::uuid
		`, j.ID, end, status, end.Sub(start).Milliseconds())
	}
	return execErr
}

// SimulateNext returns the next 3 fire times for a schedule expression. Used
// by Studio's "natural language schedule input" preview.
func (s *Scheduler) SimulateNext(schedule string, count int) ([]time.Time, error) {
	sched, err := s.parser.Parse(schedule)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 3
	}
	// Iterate in the scheduler's location so the previewed fire times land on
	// the boss's wall clock (robfig advances the schedule relative to the
	// passed time's location). The returned times carry that zone; the API
	// serialises them RFC3339 with the correct offset.
	now := time.Now().In(s.Location())
	out := make([]time.Time, 0, count)
	for i := 0; i < count; i++ {
		next := sched.Next(now)
		out = append(out, next)
		now = next
	}
	return out, nil
}

// nextFire computes the next time a schedule fires, in the scheduler's
// configured location. Persisted to mem_crons.next_run_at so the dashboard
// "Queued" column (which filters enabled crons on next_run_at > now) can show
// upcoming runs. Returns an error only for an unparseable schedule.
func (s *Scheduler) nextFire(schedule string) (time.Time, error) {
	sched, err := s.parser.Parse(schedule)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(time.Now().In(s.Location())), nil
}

// jsonOrEmpty marshals v or returns "[]" / "{}" depending on the kind.
func jsonOrEmpty(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

var _ = jsonOrEmpty // reserved for future use
