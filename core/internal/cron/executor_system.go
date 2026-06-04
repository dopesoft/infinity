package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/inbox"
	"github.com/dopesoft/infinity/core/internal/maintenance"
)

type SystemExecutor struct {
	Deps  maintenance.Deps
	Inbox inbox.Deps // deterministic inbox-triage skill (fetch → 1 LLM decide → surface)
}

func NewSystemExecutor(deps maintenance.Deps) *SystemExecutor {
	return &SystemExecutor{Deps: deps}
}

// SetInbox wires the deterministic inbox-triage skill's dependencies. Called
// from serve.go after the connectors/LLM/surface pieces exist.
func (e *SystemExecutor) SetInbox(d inbox.Deps) {
	if e == nil {
		return
	}
	e.Inbox = d
}

func (e *SystemExecutor) ExecuteJob(j Job) (RunSummary, error) {
	if e == nil {
		return RunSummary{}, errors.New("system executor not configured")
	}
	var cfg struct {
		Task    string          `json:"task"`
		Options json.RawMessage `json:"options"`
	}
	if len(j.TargetConfig) > 0 {
		_ = json.Unmarshal(j.TargetConfig, &cfg)
	}
	task := cfg.Task
	if task == "" {
		task = j.Target
	}
	switch task {
	case "nightly_cognition":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		// Stop discarding the Report — its per-op narrative is the whole point
		// of the run. Surface it on the mem_runs row (Summary) and stash the
		// full struct in meta so the run detail can show every stage count.
		rep, err := maintenance.RunNightlyCognition(ctx, e.Deps, maintenance.ParseOptions(cfg.Options))
		return RunSummary{
			Summary: rep.Summary(),
			Meta:    map[string]any{"report": rep},
		}, err
	case "inbox_triage":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var ic inbox.Config
		if len(j.TargetConfig) > 0 {
			_ = json.Unmarshal(j.TargetConfig, &ic)
		}
		// Use the scheduler's pre-stamped session id for the step plan so the run
		// row (which already carries this id from Begin) and the plan collapse
		// into one live step-timeline card on the board while the job runs.
		ic.SessionID = j.RunSessionID
		s, err := inbox.Run(ctx, e.Inbox, ic)
		return RunSummary{
			Summary: fmt.Sprintf("Triaged %d email(s) across %d mailbox(es); surfaced %d needing your reply.",
				s.Fetched, s.Accounts, s.Surfaced),
			// session_id ties this cron run to the step plan so the board folds
			// them into one card (the plan's step timeline) instead of two.
			Meta: map[string]any{"accounts": s.Accounts, "fetched": s.Fetched, "surfaced": s.Surfaced, "session_id": s.SessionID},
		}, err
	default:
		return RunSummary{}, fmt.Errorf("unknown system task %q", task)
	}
}
