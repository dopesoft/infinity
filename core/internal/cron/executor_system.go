package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/httpx"
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
		// Tag every outbound HTTP call this run makes with its session so a hard
		// failure (401/5xx) is attributed to this run and vetoes a green outcome.
		ctx = httpx.WithSession(ctx, j.RunSessionID)
		// Stop discarding the Report — its per-op narrative is the whole point
		// of the run. Surface it on the mem_runs row (Summary) and stash the
		// full struct in meta so the run detail can show every stage count.
		rep, err := maintenance.RunNightlyCognition(ctx, e.Deps, maintenance.ParseOptions(cfg.Options))
		// The executor knows its own internals better than the generic
		// classifier can: a quiet night that changed nothing is "nothing
		// needed", a night that touched memory is "did work". A stage failure
		// surfaces via err → the classifier's "failed".
		outcome := OutcomeDidWork
		if !rep.Changed() {
			outcome = OutcomeNothingNeeded
		}
		return RunSummary{
			Summary: rep.Summary(),
			Meta:    map[string]any{"report": rep, "outcome": string(outcome)},
		}, err
	case "inbox_triage":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ctx = httpx.WithSession(ctx, j.RunSessionID)
		var ic inbox.Config
		if len(j.TargetConfig) > 0 {
			_ = json.Unmarshal(j.TargetConfig, &ic)
		}
		// Use the scheduler's pre-stamped session id for the step plan so the run
		// row (which already carries this id from Begin) and the plan collapse
		// into one live step-timeline card on the board while the job runs.
		ic.SessionID = j.RunSessionID
		s, err := inbox.Run(ctx, e.Inbox, ic)
		// An empty mailbox is "nothing needed"; anything fetched is work done.
		// A BLIND run (couldn't read any mailbox) returns an error — never let the
		// rosy "no new mail" Human() line stand on a failed row; the err carries the
		// real reason and classifyOutcome marks the run failed → surfaced + pinged.
		// (Surfaced follow-ups live in the Follow-ups card, not as a decision
		// gate, so triage is never "needs_you".)
		summaryText := s.Human()
		triageOutcome := OutcomeDidWork
		switch {
		case err != nil:
			summaryText = err.Error()
		case s.Fetched == 0:
			triageOutcome = OutcomeNothingNeeded
		}
		return RunSummary{
			// Human() names actual senders/subjects ("…including Namecheap
			// about your domain renewal") — the boss reads this, not counters.
			Summary: summaryText,
			// session_id ties this cron run to the step plan so the board folds
			// them into one card (the plan's step timeline) instead of two.
			Meta: map[string]any{"accounts": s.Accounts, "fetched": s.Fetched, "surfaced": s.Surfaced, "session_id": s.SessionID, "outcome": string(triageOutcome)},
		}, err
	default:
		return RunSummary{}, fmt.Errorf("unknown system task %q", task)
	}
}
