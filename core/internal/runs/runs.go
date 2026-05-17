// Package runs is the server-side substrate for tracking long-running
// actions so the UI can show progress that persists across navigation,
// focus loss, refresh, and second-device viewing.
//
// Every long server action (cron run, skill invoke, heartbeat scan,
// voyager optimize, gym extract, sentinel dispatch, …) must wrap its
// work in Track(...). Track inserts a mem_runs row with status='running'
// before fn fires, then UPDATEs it to 'ok' / 'error' with timing + error
// when fn returns. The row is realtime-published, so Studio's useRuns()
// hook sees both transitions live.
//
// See CLAUDE.md → "Server-tracked progress" for the rule.
package runs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind is the free-string identifier for the class of action. New callers
// pick a kind and the studio filter matches on it. Established kinds:
//
//	cron              - a cron's RunOnce / scheduled fire
//	skill             - skill invoke (manual or agent-triggered)
//	heartbeat         - the heartbeat scanner
//	voyager.optimize  - GEPA optimizer run
//	voyager.extract   - skill / source extraction pass
//	gym.extract       - gym training-example extraction
//	sentinel          - sentinel dispatch
type Kind string

const (
	KindCron            Kind = "cron"
	KindSkill           Kind = "skill"
	KindHeartbeat       Kind = "heartbeat"
	KindVoyagerOptimize Kind = "voyager.optimize"
	KindVoyagerExtract  Kind = "voyager.extract"
	KindGymExtract      Kind = "gym.extract"
	KindSentinel        Kind = "sentinel"
)

// Source identifies who initiated the run. Drives Studio's "manual vs
// scheduled" filtering and audit attribution.
type Source string

const (
	SourceManual    Source = "manual"
	SourceScheduled Source = "scheduled"
	SourceAgent     Source = "agent"
	SourceHeartbeat Source = "heartbeat"
	SourceSentinel  Source = "sentinel"
)

// Tracker is the package-level handle. Stash one on package init from
// serve.go with the shared pgx pool. nil-safe: every operation no-ops
// when the pool isn't configured (eg. unit tests, migrate-only runs).
type Tracker struct {
	pool *pgxpool.Pool
}

var global *Tracker

// SetGlobal wires the package-level tracker. Call once from serve.go after
// the pool is open. Idempotent: last writer wins.
func SetGlobal(pool *pgxpool.Pool) {
	global = &Tracker{pool: pool}
}

// New returns a tracker bound to a specific pool. Tests + non-global
// callers use this; production wires the global via SetGlobal.
func New(pool *pgxpool.Pool) *Tracker {
	return &Tracker{pool: pool}
}

// Handle is returned by Begin and used to close the row when fn finishes.
// Most callers should use Track(...) which handles the begin/finish pair
// automatically; Handle is only needed for callers that need to update
// progress mid-flight or split start/finish across goroutines.
type Handle struct {
	id      string
	tracker *Tracker
	start   time.Time
}

// ID returns the mem_runs row id, mostly for logging.
func (h *Handle) ID() string {
	if h == nil {
		return ""
	}
	return h.id
}

// Track is the canonical entry point. It books a mem_runs row, runs fn,
// closes the row with the result, and returns whatever fn returned.
//
//   - kind / targetID identify what's running (eg. "cron" + cron uuid)
//   - label is the human-readable string the studio shows next to the
//     spinner ("daily-summary", "skill: resolve_connector_identity").
//   - source is who fired it (manual button, scheduler tick, agent).
//
// nil-safe: when global isn't wired, Track just runs fn directly.
func Track(ctx context.Context, kind Kind, targetID, label string, source Source, fn func(context.Context) error) error {
	return TrackWith(ctx, global, kind, targetID, label, source, fn)
}

// TrackWith is the dependency-injected form for tests and callers that
// hold a specific *Tracker. Production code uses Track.
func TrackWith(ctx context.Context, t *Tracker, kind Kind, targetID, label string, source Source, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("runs.Track: fn is nil")
	}
	handle := t.Begin(ctx, kind, targetID, label, source)
	err := fn(ctx)
	handle.Finish(ctx, err, "")
	return err
}

// Begin inserts a row with status='running' and returns a Handle. Always
// pair with Handle.Finish (use defer or a normal call). nil-safe.
func (t *Tracker) Begin(ctx context.Context, kind Kind, targetID, label string, source Source) *Handle {
	h := &Handle{tracker: t, start: time.Now().UTC()}
	if t == nil || t.pool == nil {
		return h
	}
	h.id = uuid.NewString()
	// Best-effort insert. If the insert fails we still want fn to run -
	// observability outage shouldn't break the action itself.
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_runs (id, kind, target_id, label, source, status, started_at)
		VALUES ($1::uuid, $2, $3, $4, $5, 'running', $6)
	`, h.id, string(kind), targetID, label, string(source), h.start)
	if err != nil {
		h.id = "" // mark as un-persisted so Finish can short-circuit
	}
	return h
}

// Finish closes the run with ok/error. err==nil → status='ok'. err!=nil
// → status='error' and err.Error() goes into the error column. summary
// is optional human-readable result; pass "" when there's nothing to say.
// nil-safe; safe to call twice (the second call no-ops).
func (h *Handle) Finish(ctx context.Context, err error, summary string) {
	if h == nil || h.tracker == nil || h.tracker.pool == nil || h.id == "" {
		return
	}
	end := time.Now().UTC()
	status := "ok"
	errStr := ""
	if err != nil {
		status = "error"
		errStr = err.Error()
	}
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = $2,
		       ended_at = $3,
		       duration_ms = $4,
		       error = $5,
		       result_summary = $6
		 WHERE id = $1::uuid
	`, h.id, status, end, end.Sub(h.start).Milliseconds(), errStr, summary)
	// Clear id so a second Finish call no-ops.
	h.id = ""
}

// Progress updates the optional 0..1 progress + label mid-flight. Safe
// to call zero or many times. nil-safe.
func (h *Handle) Progress(ctx context.Context, fraction float32, label string) {
	if h == nil || h.tracker == nil || h.tracker.pool == nil || h.id == "" {
		return
	}
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET progress = $2,
		       progress_label = $3
		 WHERE id = $1::uuid
	`, h.id, fraction, label)
}
