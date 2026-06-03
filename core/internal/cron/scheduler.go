package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/runs"
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
		if s.cronInFlight(ctx, j.ID) {
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
		var execErr error
		var summary RunSummary
		handle := runs.BeginGlobal(ctx, runs.KindCron, j.ID, j.Name, runs.SourceScheduled)
		if s.executor != nil {
			summary, execErr = s.executor.ExecuteJob(j)
		} else {
			execErr = errors.New("no executor configured")
		}
		handle.SetMeta(ctx, summary.Meta)
		handle.Finish(ctx, execErr, summary.Summary)

		end := time.Now().UTC()
		status := "ok"
		if execErr != nil {
			// Store the plain-language title, not the raw provider string, so
			// the cron list reads "Your model token needs reconnecting" instead
			// of "error: openai_oauth: status=401 body={...}". The full run
			// detail (raw + human) still lives on the mem_runs row.
			status = "error: " + errs.Humanize(execErr).Title
		}
		// Advance next_run_at to the upcoming fire so the dashboard "Queued"
		// column rolls forward instead of showing a stale/elapsed time.
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
		`, j.ID, end, status, end.Sub(start).Milliseconds(), execErr != nil, nextPtr)
	}
}

// cronInFlight reports whether a run of this cron is still executing, so the
// fire path can skip an overlapping concurrent run of the same schedule. Fails
// OPEN (returns false) on any query error — a rare overlap is better than a
// cron that silently never fires again because one status check hiccupped.
func (s *Scheduler) cronInFlight(ctx context.Context, cronID string) bool {
	if s == nil || s.pool == nil {
		return false
	}
	var inFlight bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM mem_runs
			 WHERE kind = $1 AND target_id = $2 AND status = 'running'
		)
	`, string(runs.KindCron), cronID).Scan(&inFlight)
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
	var execErr error
	var summary RunSummary
	// Begin/Finish exposes "this cron is running" to every device on the
	// network via mem_runs + realtime (survives navigating away from /cron or
	// closing the tab) AND persists the executor's narrative + meta onto the
	// row so the manual run reads as a report, not a bare ok. See CLAUDE.md →
	// "Server-tracked progress".
	handle := runs.BeginGlobal(ctx, runs.KindCron, j.ID, j.Name, runs.SourceManual)
	if s.executor != nil {
		summary, execErr = s.executor.ExecuteJob(j)
	} else {
		execErr = errors.New("no executor configured")
	}
	handle.SetMeta(ctx, summary.Meta)
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
