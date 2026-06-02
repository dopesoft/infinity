package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/connectors"
)

// ConnectorExecutor runs `connector_poll` jobs by decoding the row's
// target_config into a connectors.PollConfig and handing it to the
// shared Poller. No LLM. No tokens. Pure plumbing.
//
// The split mirrors the original AgentExecutor - one struct per JobKind.
// The composite executor (composite.go) chooses which one to invoke
// based on j.JobKind.
type ConnectorExecutor struct {
	Poller *connectors.Poller
}

func NewConnectorExecutor(p *connectors.Poller) *ConnectorExecutor {
	return &ConnectorExecutor{Poller: p}
}

func (e *ConnectorExecutor) ExecuteJob(j Job) (RunSummary, error) {
	if e == nil || e.Poller == nil {
		return RunSummary{}, errors.New("no connector poller wired into connector executor")
	}
	if len(j.TargetConfig) == 0 || string(j.TargetConfig) == "{}" {
		return RunSummary{}, errors.New("connector_poll job missing target_config")
	}
	var cfg connectors.PollConfig
	if err := json.Unmarshal(j.TargetConfig, &cfg); err != nil {
		return RunSummary{}, fmt.Errorf("decode target_config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Connector polls are pure plumbing with no narrative to add beyond the
	// ok/error the scheduler already records.
	_, err := e.Poller.Poll(ctx, j.Name, cfg)
	return RunSummary{}, err
}
