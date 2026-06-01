package tools

import "sync"

// run_binding maps a background sub-agent's SESSION id (eg.
// "background:<uuid>") to the mem_runs ROW id its work is booked under, so a
// native tool executing inside that loop (notably todo_write) can find the row
// to update. A tool's Execute only receives the session id via
// SessionIDFromContext; the run id lives on a *runs.Handle the background agent
// holds and never threads into the tool. This tiny registry is the bridge —
// symmetric with how ActiveSetFromContext exposes per-session tool state.
//
// It is bridge-agnostic: the binding is keyed purely by session id, so it works
// identically whether the background run executes on the Mac bridge
// (claude_code__*) or the Cloud bridge (fs_*/bash_run). The native todo_write
// tool is registered once on the shared registry and is therefore reachable in
// both bridge contexts.
type runBinding struct {
	runID    string
	hasTodos bool
}

var (
	runBindMu   sync.RWMutex
	runBindings = map[string]*runBinding{}
)

// RegisterRunForSession binds a session id to its mem_runs row id. Called by
// the background agent right after it creates the child session. No-op on
// empty input.
func RegisterRunForSession(sessionID, runID string) {
	if sessionID == "" || runID == "" {
		return
	}
	runBindMu.Lock()
	defer runBindMu.Unlock()
	runBindings[sessionID] = &runBinding{runID: runID}
}

// UnregisterRunForSession drops the binding. The background agent defers this
// so a finished run doesn't leak its mapping.
func UnregisterRunForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	runBindMu.Lock()
	defer runBindMu.Unlock()
	delete(runBindings, sessionID)
}

// RunIDForSession returns the mem_runs row id bound to a session, or "" when
// the session isn't a tracked background run.
func RunIDForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	runBindMu.RLock()
	defer runBindMu.RUnlock()
	if b := runBindings[sessionID]; b != nil {
		return b.runID
	}
	return ""
}

// MarkSessionHasTodos records that todo_write has fired at least once for this
// session's run, so the background agent stops overwriting the todo-derived
// progress with its tool-call heuristic.
func MarkSessionHasTodos(sessionID string) {
	if sessionID == "" {
		return
	}
	runBindMu.Lock()
	defer runBindMu.Unlock()
	if b := runBindings[sessionID]; b != nil {
		b.hasTodos = true
	}
}

// SessionHasTodos reports whether the session's run has a todo checklist. The
// background agent's progress heuristic checks this and stays out of the way
// once the agent is driving its own checklist.
func SessionHasTodos(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	runBindMu.RLock()
	defer runBindMu.RUnlock()
	if b := runBindings[sessionID]; b != nil {
		return b.hasTodos
	}
	return false
}
