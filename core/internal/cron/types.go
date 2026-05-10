// Package cron implements Phase 6 — scheduled jobs backed by robfig/cron/v3.
//
// A "cron" is a row in mem_crons that maps a schedule expression to either:
//   • system_event       — sends a prompt into a live session
//   • isolated_agent_turn — spawns a fresh sub-agent with its own context
//
// The scheduler loads every enabled row on boot and re-loads on Reload(). It
// degrades gracefully when no LLM provider is configured (jobs run, target
// prompt is logged, but no completion happens).
package cron

import "time"

type JobKind string

const (
	JobSystemEvent       JobKind = "system_event"
	JobIsolatedAgentTurn JobKind = "isolated_agent_turn"
)

func (k JobKind) Valid() bool {
	switch k {
	case JobSystemEvent, JobIsolatedAgentTurn:
		return true
	}
	return false
}

// Job is the row shape from mem_crons.
type Job struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Schedule        string    `json:"schedule"`
	ScheduleNatural string    `json:"schedule_natural,omitempty"`
	JobKind         JobKind   `json:"job_kind"`
	Target          string    `json:"target"`
	Enabled         bool      `json:"enabled"`
	MaxRetries      int       `json:"max_retries"`
	BackoffSeconds  int       `json:"backoff_seconds"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus   string    `json:"last_run_status,omitempty"`
	LastRunDuration int       `json:"last_run_duration_ms,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	FailureCount    int       `json:"failure_count"`
	CreatedAt       time.Time `json:"created_at"`
}

// Executor is what the scheduler hands a fired job to. The agent loop
// implements this — for system_event we Run() against the main session, for
// isolated_agent_turn we Run() in a brand-new session.
type Executor interface {
	ExecuteJob(j Job) error
}
