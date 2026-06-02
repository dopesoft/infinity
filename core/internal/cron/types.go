// Package cron implements scheduled jobs backed by robfig/cron/v3.
//
// A "cron" is a row in mem_crons that maps a schedule expression to one of
// three job kinds:
//
//   - system_event        - sends a prompt into a live session
//   - isolated_agent_turn - spawns a fresh sub-agent with its own context
//   - connector_poll      - fires a deterministic Composio tools.execute
//     call (no LLM) and projects the response into
//     a dashboard table (mem_followups / events)
//   - system_task         - fires a deterministic internal maintenance task
//
// The scheduler loads every enabled row on boot and re-loads on Reload(). It
// degrades gracefully when no LLM provider is configured (jobs run, target
// prompt is logged, but no completion happens). connector_poll jobs are
// independent of the LLM - they keep running on chat-only or no-LLM builds.
package cron

import (
	"encoding/json"
	"time"
)

type JobKind string

const (
	JobSystemEvent       JobKind = "system_event"
	JobIsolatedAgentTurn JobKind = "isolated_agent_turn"
	JobConnectorPoll     JobKind = "connector_poll"
	JobSystemTask        JobKind = "system_task"
)

func (k JobKind) Valid() bool {
	switch k {
	case JobSystemEvent, JobIsolatedAgentTurn, JobConnectorPoll, JobSystemTask:
		return true
	}
	return false
}

// Job is the row shape from mem_crons.
//
// TargetConfig is the JSONB column added in migration 015. It's only
// populated for `connector_poll` jobs (carries the Composio action +
// connected_account_id + sink). Agent-driven kinds leave it `{}` and
// continue to read their prompt from `target`.
type Job struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Schedule        string          `json:"schedule"`
	ScheduleNatural string          `json:"schedule_natural,omitempty"`
	JobKind         JobKind         `json:"job_kind"`
	Target          string          `json:"target"`
	TargetConfig    json.RawMessage `json:"target_config,omitempty"`
	Enabled         bool            `json:"enabled"`
	MaxRetries      int             `json:"max_retries"`
	BackoffSeconds  int             `json:"backoff_seconds"`
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	LastRunStatus   string          `json:"last_run_status,omitempty"`
	LastRunDuration int             `json:"last_run_duration_ms,omitempty"`
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	FailureCount    int             `json:"failure_count"`
	CreatedAt       time.Time       `json:"created_at"`
}

// RunSummary is what an executor reports back about a job it just ran, so the
// scheduler can persist a human "what I did / how it went / outcome" narrative
// (Summary → mem_runs.result_summary) plus structured detail (Meta →
// mem_runs.meta) instead of a bare ok/error. Both fields are optional: an
// executor with nothing to say returns the zero value and the run still closes
// cleanly, exactly as before. This is what lets Studio show a real report on
// every cron firing — live on the kanban card and durably in /logs — rather
// than a green checkmark with no story behind it.
type RunSummary struct {
	// Summary is the boss-facing narrative. Keep it human and first-person
	// where it comes from the agent's own closing words; the UI clamps it on
	// the card and shows it in full in the run detail.
	Summary string
	// Meta is structured detail (turn/tool counts, session id, the nightly
	// cognition Report, …) merged into mem_runs.meta verbatim. nil/empty → no
	// meta written.
	Meta map[string]any
}

// Executor is what the scheduler hands a fired job to. The agent loop
// implements this - for system_event we Run() against the main session, for
// isolated_agent_turn we Run() in a brand-new session. It returns a RunSummary
// describing what the job did (best-effort; the zero value is valid) alongside
// the error so the scheduler can record both the narrative and the outcome.
type Executor interface {
	ExecuteJob(j Job) (RunSummary, error)
}
