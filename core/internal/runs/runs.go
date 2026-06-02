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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/errs"
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
	KindBrowserSession  Kind = "browser.session"
	KindExtension       Kind = "extension.register"
	KindBackgroundBuild Kind = "background.build"
	KindSurfaceAction   Kind = "surface.action"
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

// Notifier is the optional sink runs.Track fans out to when a run begins
// or finishes. Wired from serve.go via SetNotifier so the runs package
// stays push-agnostic (push depends on runs for kinds, not the other
// way). Both methods are best-effort fire-and-forget — slow or failing
// notifications must NEVER block the work fn.
type Notifier interface {
	NotifyRunStarted(ctx context.Context, runID, kind, label string)
	NotifyRunFinished(ctx context.Context, runID, kind, label, status, errMsg string, duration time.Duration)
}

var notifier Notifier

// SetNotifier wires the package-level notifier. Idempotent: last writer
// wins, nil clears.
func SetNotifier(n Notifier) {
	notifier = n
}

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
	kind    string
	label   string
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

// BeginGlobal books a run on the package-global tracker and returns its
// Handle so a long detached job can push mid-flight Handle.Progress updates
// (the common case: a background_build whose mem_runs.progress must stay live
// for the dock to read). Pair with Handle.Finish. nil-safe when the global
// tracker is unset (returns a no-op Handle).
func BeginGlobal(ctx context.Context, kind Kind, targetID, label string, source Source) *Handle {
	return global.Begin(ctx, kind, targetID, label, source)
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
	h := &Handle{tracker: t, start: time.Now().UTC(), kind: string(kind), label: label}
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
	if notifier != nil && h.id != "" {
		go notifier.NotifyRunStarted(context.Background(), h.id, h.kind, h.label)
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
	humanJSON := "{}"
	if err != nil {
		status = "error"
		errStr = err.Error()
		// Translate the raw error into a boss-facing explanation alongside the
		// raw string, so the UI can show "your model token was revoked" instead
		// of the provider's JSON. Best-effort: a marshal failure just leaves
		// human_error empty, never blocks the row close.
		if b, mErr := json.Marshal(errs.HumanizeString(errStr)); mErr == nil {
			humanJSON = string(b)
		}
	}
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = $2,
		       ended_at = $3,
		       duration_ms = $4,
		       error = $5,
		       result_summary = $6,
		       human_error = $7::jsonb
		 WHERE id = $1::uuid
	`, h.id, status, end, end.Sub(h.start).Milliseconds(), errStr, summary, humanJSON)
	if notifier != nil {
		go notifier.NotifyRunFinished(context.Background(), h.id, h.kind, h.label, status, errStr, end.Sub(h.start))
	}
	// Clear id so a second Finish call no-ops.
	h.id = ""
}

// RecoverStranded closes every mem_runs row still marked 'running' at boot.
// A 'running' row was booked by Begin in a previous process that has since
// died (deploy, crash, OOM); the in-process Handle that would have called
// Finish is gone, so without this sweep the row spins forever in the UI (the
// exact symptom: a nightly-cognition run stuck 'running' for hours). Core is
// a single Railway instance, so any 'running' row visible at startup is
// definitively orphaned. Mirrors memory.TurnStore.RecoverStranded; call it
// once at boot. Returns the number of rows closed.
func (t *Tracker) RecoverStranded(ctx context.Context) (int, error) {
	if t == nil || t.pool == nil {
		return 0, nil
	}
	tag, err := t.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = 'error',
		       ended_at = COALESCE(ended_at, NOW()),
		       duration_ms = COALESCE(duration_ms,
		           LEAST(2147483647, GREATEST(0,
		               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
		       error = COALESCE(NULLIF(error, ''), 'core restarted while run was in flight'),
		       result_summary = COALESCE(NULLIF(result_summary, ''), '(interrupted by restart)')
		 WHERE status = 'running'
	`)
	if err != nil {
		return 0, fmt.Errorf("recover stranded runs: %w", err)
	}
	return int(tag.RowsAffected()), nil
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

// SetMetaString sets meta.<key> = <value> (a string) on the run row,
// preserving every other key in the JSONB blob. Used by the background agent
// to record the current file being touched (meta.currentFile) alongside the
// agent-authored meta.todos / meta.repo. Best-effort + nil-safe; an empty
// value is a no-op so we don't blank a useful field with noise.
func (h *Handle) SetMetaString(ctx context.Context, key, value string) {
	if h == nil || h.tracker == nil || h.tracker.pool == nil || h.id == "" || key == "" || value == "" {
		return
	}
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET meta = jsonb_set(COALESCE(meta,'{}'::jsonb), ARRAY[$2], to_jsonb($3::text), true)
		 WHERE id = $1::uuid
	`, h.id, key, value)
}
