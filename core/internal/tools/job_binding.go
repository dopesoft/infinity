package tools

import (
	"strings"
	"sync"
)

// job_binding maps an agent turn's SESSION id to the NAME of the job that
// launched it — today that's a cron's name ("inbox-triage"). It exists so the
// identity of a scheduled job flows all the way down to the plan the agent
// builds to carry it out, instead of the plan inventing its own headline.
//
// The chain is: a cron (the named job) spawns an isolated_agent_turn; inside
// that turn the agent invokes one or more skills and calls plan_create to track
// its steps. Without this binding, plan_create lets the model type any title,
// so a nightly "inbox-triage" cron produced a plan titled "Gmail triage across
// all connected mailboxes" — same job, two names, two cards on the Agent Work
// board. With it, plan_create inherits the job name as the plan's headline so
// the cron, the card, and the plan all read the same thing.
//
// Per Rule #1b this is a MECHANIC, not a prose instruction the model can drop:
// the executor registers the job name, plan_create reads it, no judgment
// involved. Keyed purely by session id and symmetric with RegisterRunForSession.
var (
	jobBindMu   sync.RWMutex
	jobBindings = map[string]string{}
)

// RegisterJobForSession binds a session id to the launching job's name. Called
// by the cron executor right before it runs the agent turn. No-op on empty input.
func RegisterJobForSession(sessionID, jobName string) {
	if sessionID == "" || strings.TrimSpace(jobName) == "" {
		return
	}
	jobBindMu.Lock()
	defer jobBindMu.Unlock()
	jobBindings[sessionID] = strings.TrimSpace(jobName)
}

// JobForSession returns the launching job's name for a session, or "" when the
// session wasn't launched by a tracked job (e.g. an ordinary boss chat). Read by
// plan_create to inherit the headline.
func JobForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	jobBindMu.RLock()
	defer jobBindMu.RUnlock()
	return jobBindings[sessionID]
}

// UnregisterJobForSession drops the binding. The executor defers this after the
// turn so the map can't grow unbounded across fires.
func UnregisterJobForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	jobBindMu.Lock()
	defer jobBindMu.Unlock()
	delete(jobBindings, sessionID)
}

// humanizeJobName turns a cron/job slug into a readable headline:
// "inbox-triage" -> "Inbox triage", "nightly_cognition" -> "Nightly cognition".
// Mirrors the dashboard's humanizeName so a plan title inherited from a job
// reads the same as the job's own card.
func humanizeJobName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}
