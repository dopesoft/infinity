package cron

import "fmt"

// CompositeExecutor dispatches a fired job to the per-kind executor based
// on j.JobKind. Either field can be nil; missing handlers return a clear
// error so cron's last_run_status carries visibility.
//
//	system_event / isolated_agent_turn → Agent
//	connector_poll                      → Connector
//	system_task                         → System
//
// The scheduler holds a single cron.Executor (Scheduler.executor). When
// the build needs both LLM-driven jobs and connector polls, wire a
// CompositeExecutor and pass it into cron.New.
type CompositeExecutor struct {
	Agent     Executor
	Connector Executor
	System    Executor
}

func NewCompositeExecutor(agent, connector Executor, system ...Executor) *CompositeExecutor {
	var sys Executor
	if len(system) > 0 {
		sys = system[0]
	}
	return &CompositeExecutor{Agent: agent, Connector: connector, System: sys}
}

func (c *CompositeExecutor) ExecuteJob(j Job) error {
	switch j.JobKind {
	case JobConnectorPoll:
		if c.Connector == nil {
			return fmt.Errorf("composite: no connector executor configured for kind=%s", j.JobKind)
		}
		return c.Connector.ExecuteJob(j)
	case JobSystemTask:
		if c.System == nil {
			return fmt.Errorf("composite: no system executor configured for kind=%s", j.JobKind)
		}
		return c.System.ExecuteJob(j)
	case JobSystemEvent, JobIsolatedAgentTurn:
		if c.Agent == nil {
			return fmt.Errorf("composite: no agent executor configured for kind=%s", j.JobKind)
		}
		return c.Agent.ExecuteJob(j)
	default:
		return fmt.Errorf("composite: unknown job_kind %q", j.JobKind)
	}
}
