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

func (e *SystemExecutor) ExecuteJob(j Job) error {
	if e == nil {
		return errors.New("system executor not configured")
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
		_, err := maintenance.RunNightlyCognition(ctx, e.Deps, maintenance.ParseOptions(cfg.Options))
		return err
	default:
		return fmt.Errorf("unknown system task %q", task)
	}
}
