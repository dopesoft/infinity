package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/maintenance"
)

type SystemExecutor struct {
	Deps maintenance.Deps
}

func NewSystemExecutor(deps maintenance.Deps) *SystemExecutor {
	return &SystemExecutor{Deps: deps}
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
	default:
		return RunSummary{}, fmt.Errorf("unknown system task %q", task)
	}
}
