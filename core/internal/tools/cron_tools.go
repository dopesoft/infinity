// Cron tools - let Jarvis schedule recurring work from chat.
//
// Six tools land here:
//
//   cron_create_agent    - schedule an isolated_agent_turn job (prompt cron)
//   cron_create_poll     - schedule a connector_poll job (Composio action cron)
//   cron_list            - list every cron row
//   cron_delete          - remove a cron row by id or name
//   cron_pause           - toggle enabled flag without deleting
//   cron_run_now         - fire a job immediately (debug + on-demand)
//
// These wrap the existing cron.Scheduler (same package the HTTP API uses).
// Designed to be the "Hey Jarvis, check my email every 15 minutes" path:
// the model picks cron_create_poll with a sensible default for action +
// connected_account_id, the row lands in mem_crons, the scheduler picks it
// up on next reload, and it runs forever until the boss says stop.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CronJob is the minimal shape we need from a scheduled cron row. Mirrors
// cron.Job but lives here so the tools package doesn't import the cron
// package (which would create a cycle: cron → agent → tools).
type CronJob struct {
	ID              string
	Name            string
	Schedule        string
	ScheduleNatural string
	JobKind         string
	Target          string
	TargetConfig    json.RawMessage
	Enabled         bool
	MaxRetries      int
	BackoffSeconds  int
	LastRunStatus   string
}

// CronScheduler is the contract the tools rely on. The concrete type
// (cron.Scheduler) satisfies it through a thin adapter constructed in
// serve.go - see cronSchedulerAdapter there. Keeping the interface
// here breaks the import cycle.
type CronScheduler interface {
	Upsert(ctx context.Context, j CronJob) (string, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]CronJob, error)
	RunOnce(ctx context.Context, j CronJob) error
	Reload(ctx context.Context) error
}

// Cron kind constants - local mirror so tools don't import cron.
const (
	cronKindAgent = "isolated_agent_turn"
	cronKindPoll  = "connector_poll"
)

// RegisterCronTools wires every cron management tool. No-op if scheduler
// is nil so chat-only / no-DB deployments don't break tool registration.
func RegisterCronTools(r *Registry, scheduler CronScheduler, pool *pgxpool.Pool) {
	if r == nil || scheduler == nil {
		return
	}
	r.Register(&cronCreateAgent{sched: scheduler})
	r.Register(&cronCreatePoll{sched: scheduler})
	r.Register(&cronList{sched: scheduler})
	r.Register(&cronDelete{sched: scheduler, pool: pool})
	r.Register(&cronPause{sched: scheduler, pool: pool})
	r.Register(&cronRunNow{sched: scheduler, pool: pool})
	_ = time.Second // reserved
}

// ── cron_create_agent ─────────────────────────────────────────────────────

type cronCreateAgent struct{ sched CronScheduler }

func (t *cronCreateAgent) Name() string { return "cron_create_agent" }
func (t *cronCreateAgent) Description() string {
	return "Schedule a recurring agent turn. The cron expression fires a fresh sub-agent that runs the given prompt. Use for 'every morning, draft me a digest' style recurring work."
}
func (t *cronCreateAgent) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":             map[string]any{"type": "string", "description": "Unique name (kebab-case). Reused to update an existing cron."},
			"schedule":         map[string]any{"type": "string", "description": "5-field cron expression in UTC (e.g. '*/15 * * * *', '0 9 * * 1-5')."},
			"schedule_natural": map[string]any{"type": "string", "description": "Optional human description ('every 15 min', 'weekdays 9am UTC')."},
			"prompt":           map[string]any{"type": "string", "description": "Prompt the sub-agent runs each fire."},
		},
		"required": []string{"name", "schedule", "prompt"},
	}
}
func (t *cronCreateAgent) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strString(in, "name")
	sched := strString(in, "schedule")
	prompt := strString(in, "prompt")
	if name == "" || sched == "" || prompt == "" {
		return "", errors.New("name, schedule, and prompt are required")
	}
	id, err := t.sched.Upsert(ctx, CronJob{
		Name:            name,
		Schedule:        sched,
		ScheduleNatural: strString(in, "schedule_natural"),
		JobKind:         cronKindAgent,
		Target:          prompt,
		Enabled:         true,
		MaxRetries:      3,
		BackoffSeconds:  60,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","name":"%s","kind":"agent"}`, id, name), nil
}

// ── cron_create_poll ──────────────────────────────────────────────────────

type cronCreatePoll struct{ sched CronScheduler }

func (t *cronCreatePoll) Name() string { return "cron_create_poll" }
func (t *cronCreatePoll) Description() string {
	return "Schedule a recurring Composio action poll (no LLM). Use to populate the dashboard from Gmail, Calendar, etc. Pick `action` from the toolkit's published action slugs (e.g. GMAIL_FETCH_EMAILS, GOOGLECALENDAR_EVENTS_LIST). `sink` decides which dashboard table the response writes into."
}
func (t *cronCreatePoll) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":                 map[string]any{"type": "string", "description": "Unique name (kebab-case)."},
			"schedule":             map[string]any{"type": "string", "description": "5-field cron expression UTC (e.g. '*/15 * * * *')."},
			"schedule_natural":     map[string]any{"type": "string", "description": "Optional human description."},
			"toolkit":              map[string]any{"type": "string", "description": "Composio toolkit slug ('gmail', 'googlecalendar', ...). Used for source labeling."},
			"action":               map[string]any{"type": "string", "description": "Composio action slug ('GMAIL_FETCH_EMAILS', 'GOOGLECALENDAR_EVENTS_LIST', ...)."},
			"connected_account_id": map[string]any{"type": "string", "description": "Composio connected_account id (ca_...). Pull from the <connected_accounts> block in your system prompt."},
			"arguments":            map[string]any{"type": "object", "description": "Action arguments object (e.g. {\"max_results\": 10, \"query\": \"is:unread\"}). Optional."},
			"sink":                 map[string]any{"type": "string", "enum": []string{"followups", "calendar"}, "description": "Which dashboard table to write into: followups (Gmail-style messages) or calendar (events)."},
		},
		"required": []string{"name", "schedule", "toolkit", "action", "connected_account_id", "sink"},
	}
}
func (t *cronCreatePoll) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strString(in, "name")
	sched := strString(in, "schedule")
	toolkit := strString(in, "toolkit")
	action := strString(in, "action")
	caID := strString(in, "connected_account_id")
	sink := strString(in, "sink")
	if name == "" || sched == "" || action == "" || caID == "" || sink == "" {
		return "", errors.New("name, schedule, action, connected_account_id, and sink are required")
	}
	var args map[string]any
	if v, ok := in["arguments"].(map[string]any); ok {
		args = v
	}
	cfg := connectors.PollConfig{
		Toolkit:            toolkit,
		Action:             action,
		ConnectedAccountID: caID,
		Arguments:          args,
		Sink:               sink,
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	id, err := t.sched.Upsert(ctx, CronJob{
		Name:            name,
		Schedule:        sched,
		ScheduleNatural: strString(in, "schedule_natural"),
		JobKind:         cronKindPoll,
		Target:          fmt.Sprintf("%s/%s → %s", toolkit, action, sink),
		TargetConfig:    cfgJSON,
		Enabled:         true,
		MaxRetries:      3,
		BackoffSeconds:  60,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","name":"%s","kind":"connector_poll"}`, id, name), nil
}

// ── cron_list ─────────────────────────────────────────────────────────────

type cronList struct{ sched CronScheduler }

func (t *cronList) Name() string { return "cron_list" }
func (t *cronList) Description() string {
	return "List every scheduled cron job (agent prompts + connector polls). Returns id, name, schedule, kind, enabled, last_run_status."
}
func (t *cronList) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *cronList) Execute(ctx context.Context, _ map[string]any) (string, error) {
	jobs, err := t.sched.List(ctx)
	if err != nil {
		return "", err
	}
	type row struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Schedule        string `json:"schedule"`
		ScheduleNatural string `json:"schedule_natural,omitempty"`
		Kind            string `json:"kind"`
		Enabled         bool   `json:"enabled"`
		LastStatus      string `json:"last_run_status,omitempty"`
	}
	out := make([]row, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, row{
			ID: j.ID, Name: j.Name, Schedule: j.Schedule,
			ScheduleNatural: j.ScheduleNatural,
			Kind:            string(j.JobKind), Enabled: j.Enabled,
			LastStatus: j.LastRunStatus,
		})
	}
	b, _ := json.Marshal(map[string]any{"crons": out})
	return string(b), nil
}

// ── cron_delete ───────────────────────────────────────────────────────────

type cronDelete struct {
	sched CronScheduler
	pool  *pgxpool.Pool
}

func (t *cronDelete) Name() string { return "cron_delete" }
func (t *cronDelete) Description() string {
	return "Delete a cron job by id OR name. Returns the resolved id deleted."
}
func (t *cronDelete) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "string", "description": "UUID of the cron row. Optional if name is given."},
			"name": map[string]any{"type": "string", "description": "Unique name of the cron row. Optional if id is given."},
		},
	}
}
func (t *cronDelete) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	name := strString(in, "name")
	if id == "" && name == "" {
		return "", errors.New("id or name required")
	}
	if id == "" {
		resolved, err := resolveCronIDByName(ctx, t.pool, name)
		if err != nil {
			return "", err
		}
		id = resolved
	}
	if err := t.sched.Delete(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s"}`, id), nil
}

// ── cron_pause ────────────────────────────────────────────────────────────

type cronPause struct {
	sched CronScheduler
	pool  *pgxpool.Pool
}

func (t *cronPause) Name() string { return "cron_pause" }
func (t *cronPause) Description() string {
	return "Pause or resume a cron job. `enabled=false` stops it from firing without deleting; `enabled=true` resumes."
}
func (t *cronPause) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"name":    map[string]any{"type": "string"},
			"enabled": map[string]any{"type": "boolean", "description": "false = pause, true = resume."},
		},
		"required": []string{"enabled"},
	}
}
func (t *cronPause) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	name := strString(in, "name")
	enabled, ok := in["enabled"].(bool)
	if !ok {
		return "", errors.New("enabled (boolean) required")
	}
	if id == "" && name == "" {
		return "", errors.New("id or name required")
	}
	if t.pool == nil {
		return "", errors.New("no pool wired into cron_pause")
	}
	// Apply directly to the DB then ask scheduler to reload, mirroring the
	// pattern in Upsert/Delete (the scheduler reads the row state on Reload).
	var (
		err     error
		whereID string
	)
	if id != "" {
		whereID = id
		_, err = t.pool.Exec(ctx, `UPDATE mem_crons SET enabled = $2 WHERE id = $1::uuid`, id, enabled)
	} else {
		whereID, err = resolveCronIDByName(ctx, t.pool, name)
		if err == nil {
			_, err = t.pool.Exec(ctx, `UPDATE mem_crons SET enabled = $2 WHERE id = $1::uuid`, whereID, enabled)
		}
	}
	if err != nil {
		return "", err
	}
	if err := t.sched.Reload(ctx); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","enabled":%v}`, whereID, enabled), nil
}

// ── cron_run_now ──────────────────────────────────────────────────────────

type cronRunNow struct {
	sched CronScheduler
	pool  *pgxpool.Pool
}

func (t *cronRunNow) Name() string { return "cron_run_now" }
func (t *cronRunNow) Description() string {
	return "Fire a cron job once immediately, regardless of its schedule. Useful to verify a fresh setup or refresh a poll on demand. Does NOT mutate the schedule."
}
func (t *cronRunNow) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "string"},
			"name": map[string]any{"type": "string"},
		},
	}
}
func (t *cronRunNow) Execute(ctx context.Context, in map[string]any) (string, error) {
	if t.pool == nil {
		return "", errors.New("no pool wired into cron_run_now")
	}
	id := strString(in, "id")
	name := strString(in, "name")
	if id == "" && name == "" {
		return "", errors.New("id or name required")
	}
	jobs, err := t.sched.List(ctx)
	if err != nil {
		return "", err
	}
	var match *CronJob
	for i := range jobs {
		j := &jobs[i]
		if (id != "" && j.ID == id) || (name != "" && j.Name == name) {
			match = j
			break
		}
	}
	if match == nil {
		return "", fmt.Errorf("cron not found (id=%q, name=%q)", id, name)
	}
	if err := t.sched.RunOnce(ctx, *match); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","name":"%s","fired":true}`, match.ID, match.Name), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func resolveCronIDByName(ctx context.Context, pool *pgxpool.Pool, name string) (string, error) {
	if pool == nil {
		return "", errors.New("no pool")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name empty")
	}
	var id string
	err := pool.QueryRow(ctx, `SELECT id::text FROM mem_crons WHERE name = $1`, name).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("cron %q not found: %w", name, err)
	}
	return id, nil
}
