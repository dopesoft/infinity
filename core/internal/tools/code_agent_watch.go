package tools

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/watch"
)

// Reporting back on a DETACHED coding job.
//
// When a coding job outlives its turn (the boss spoke, or the 15-minute chat
// turn timed out), the tool returns "still running" and the job keeps going.
// That is only half an answer: the boss must be TOLD when it lands, without
// asking. The substrate for that already exists - mem_watches + watch.Poller,
// the same one behind watch_until - so nothing new is built here; the detach
// path just registers a watch on its own run id.
//
// Two ways in, in preference order, because code_agent is registered at
// serve.go:501 and the watch store only exists at serve.go:1690:
//
//  1. AttachWatchCreator, the direct seam - one line in serve.go.
//  2. The registered `watch_until` tool, looked up on the same registry at
//     call time. This needs NO wiring at all, which is why it exists: a
//     capability that depends on a line someone still has to add is not
//     built (CLAUDE.md Rule #1c).
//
// Both end at watch.Store.Create. The watch poller then delivers ONE
// follow-up into the session the moment the run row leaves 'running'.

// codeAgentWatchTimeout bounds the watch. The job itself can run for
// codeAgentMaxWait; the follower closes the run row shortly after that, so a
// little more than the ceiling is enough for the watch to see it settle.
const codeAgentWatchTimeout = codeAgentMaxWait + 2*codeAgentJobGrace

// AttachWatchCreator hands code_agent the watch store directly, so a detached
// job registers its report-back without going through the watch_until tool.
// Optional: without it the registry fallback is used.
//
// Wiring (one line, next to tools.RegisterWatchTools in serve.go):
//
//	tools.AttachWatchCreator(registry, watchStore)
func AttachWatchCreator(r *Registry, store WatchCreator) {
	if r == nil || store == nil {
		return
	}
	if t, ok := r.Get("code_agent"); ok {
		if ca, ok := t.(*codeAgent); ok {
			ca.watches = store
		}
	}
}

// watchDetached registers the "tell him when it lands" watch for a job that
// outlived its turn, and records on `still` whether it actually exists - the
// tool result only promises a callback that a real row backs.
func (t *codeAgent) watchDetached(ctx context.Context, runID, label string, still *stillRunningError) {
	sessionID := SessionIDFromContext(ctx)
	// mem_watches.session_id is a uuid column: a synthetic id (a delegate, a
	// background job) would be a 22P02, and there is no chat to deliver into
	// anyway.
	if !isPlainSessionID(sessionID) || strings.TrimSpace(runID) == "" {
		return
	}
	// A fresh context: the turn's is usually already dead by the time we
	// detach, which is the whole reason this callback has to be durable.
	wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := t.createWatch(wctx, sessionID, runID, label); err != nil {
		// Not fatal: the job still runs and its run row still closes with the
		// receipt. But it IS a real failure of our own code, so it is named
		// on stderr rather than swallowed into a rosy "I'll let you know".
		log.Printf("code_agent: could not register the report-back watch for run %s: %v", runID, err)
		return
	}
	still.reporting = true
	codeAgentInfo().Printf("code_agent: watching detached run %s to report back into session %s", runID, sessionID)
}

// createWatch is the one place the two routes converge.
func (t *codeAgent) createWatch(ctx context.Context, sessionID, runID, label string) error {
	if strings.TrimSpace(label) == "" {
		label = "the coding job"
	}
	note := "This is the Claude Code job that kept running after the boss's message. Report what it actually did."
	if t.watches != nil {
		_, err := t.watches.Create(ctx, watch.Watch{
			SessionID:   sessionID,
			Kind:        "run",
			TargetID:    runID,
			TargetLabel: label,
			Note:        note,
			Timeout:     codeAgentWatchTimeout,
		})
		return err
	}
	if t.reg == nil {
		return errors.New("no watch store and no registry to reach one")
	}
	wt, ok := t.reg.Get("watch_until")
	if !ok {
		return errors.New("watch_until is not registered (no database?), so nothing can report back")
	}
	_, err := wt.Execute(WithSessionID(ctx, sessionID), map[string]any{
		"run_id":          runID,
		"label":           label,
		"note":            note,
		"timeout_minutes": int(codeAgentWatchTimeout / time.Minute),
	})
	return err
}
