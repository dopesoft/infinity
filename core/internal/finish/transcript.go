package finish

import (
	"context"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// BridgeTranscript reads a coding job's own files off whichever bridge serves
// the session, through the router's normal failover.
//
// It is a thin adapter on purpose. The parsing — what a terminal `result`
// looks like, which files the stream shows being written — lives in `tools`
// beside the poll loop that writes those files, so there is ONE definition of
// "this job finished" and not a second one here that can drift from it.
type BridgeTranscript struct {
	router *bridge.Router
	prefs  PreferenceFor
}

// NewBridgeTranscript returns a reader, or nil without a router (the poller
// then continues stranded work without being able to spot the finished ones).
func NewBridgeTranscript(router *bridge.Router, prefs PreferenceFor) *BridgeTranscript {
	if router == nil {
		return nil
	}
	return &BridgeTranscript{router: router, prefs: prefs}
}

// Read probes the job and reports what its files say. Read-only: it never
// kills the process it finds, never deletes a transcript, never writes.
func (t *BridgeTranscript) Read(ctx context.Context, sessionID, repo, runID string) Verdict {
	if t == nil || t.router == nil {
		return Verdict{Err: "no bridge router is configured"}
	}
	pref := bridge.PrefAuto
	if t.prefs != nil {
		pref = t.prefs(ctx, sessionID)
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var v tools.ClaudeJobVerdict
	_, _, _, _, err := t.router.Call(rctx, pref, func(b bridge.Bridge) ([]byte, int, bool) {
		v = tools.ReadClaudeJobVerdict(rctx, b, repo, runID)
		if !v.Looked() {
			// Report it as a transport failure so the router's own failover
			// gets its turn: a sleeping Mac should mean the cloud answers,
			// not "the job left nothing behind".
			return nil, 0, false
		}
		return nil, 200, true
	})
	if err != nil {
		return Verdict{Err: "the bridge could not be reached (" + err.Error() + ")"}
	}
	if !v.Looked() {
		return Verdict{Err: v.Err}
	}
	return Verdict{
		Found:   v.Found,
		Alive:   v.Alive,
		Done:    v.Done,
		IsError: v.IsError,
		Report:  v.Report,
		Files:   v.Files,
	}
}
