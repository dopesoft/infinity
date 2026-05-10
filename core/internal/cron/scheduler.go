package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	robfig "github.com/robfig/cron/v3"
)

// Scheduler wraps robfig/cron/v3 with DB-backed job state.
type Scheduler struct {
	pool     *pgxpool.Pool
	executor Executor
	cron     *robfig.Cron
	parser   robfig.Parser

	mu      sync.Mutex
	entries map[string]robfig.EntryID // job ID → robfig entry
}

func New(pool *pgxpool.Pool, exec Executor) *Scheduler {
	parser := robfig.NewParser(
		robfig.Minute | robfig.Hour | robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor,
	)
	c := robfig.New(robfig.WithParser(parser), robfig.WithLocation(time.UTC))
	return &Scheduler{
		pool:     pool,
		executor: exec,
		cron:     c,
		parser:   parser,
		entries:  make(map[string]robfig.EntryID),
	}
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
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.ScheduleNatural, &kind,
			&j.Target, &j.Enabled, &j.MaxRetries, &j.BackoffSeconds,
			&j.LastRunAt, &j.LastRunStatus, &j.LastRunDuration,
			&j.NextRunAt, &j.FailureCount, &j.CreatedAt); err != nil {
			return err
		}
		j.JobKind = JobKind(kind)
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
	}
	return nil
}

func (s *Scheduler) makeFireFn(j Job) func() {
	return func() {
		start := time.Now().UTC()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		var execErr error
		if s.executor != nil {
			execErr = s.executor.ExecuteJob(j)
		} else {
			execErr = errors.New("no executor configured")
		}

		end := time.Now().UTC()
		status := "ok"
		if execErr != nil {
			status = "error: " + execErr.Error()
		}
		_, _ = s.pool.Exec(ctx, `
			UPDATE mem_crons SET
			  last_run_at = $2,
			  last_run_status = $3,
			  last_run_duration_ms = $4,
			  failure_count = CASE WHEN $5 THEN failure_count + 1 ELSE 0 END
			 WHERE id = $1::uuid
		`, j.ID, end, status, end.Sub(start).Milliseconds(), execErr != nil)
	}
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
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_crons (id, name, schedule, schedule_natural, job_kind, target,
		                       enabled, max_retries, backoff_seconds)
		VALUES ($1::uuid, $2, $3, NULLIF($4,''), $5, $6, $7, $8, $9)
		ON CONFLICT (name) DO UPDATE SET
		  schedule = EXCLUDED.schedule,
		  schedule_natural = EXCLUDED.schedule_natural,
		  job_kind = EXCLUDED.job_kind,
		  target = EXCLUDED.target,
		  enabled = EXCLUDED.enabled
	`, j.ID, j.Name, j.Schedule, j.ScheduleNatural, string(j.JobKind), j.Target,
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
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.ScheduleNatural, &kind,
			&j.Target, &j.Enabled, &j.MaxRetries, &j.BackoffSeconds,
			&j.LastRunAt, &j.LastRunStatus, &j.LastRunDuration,
			&j.NextRunAt, &j.FailureCount, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.JobKind = JobKind(kind)
		out = append(out, j)
	}
	return out, rows.Err()
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
	now := time.Now().UTC()
	out := make([]time.Time, 0, count)
	for i := 0; i < count; i++ {
		next := sched.Next(now)
		out = append(out, next)
		now = next
	}
	return out, nil
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
