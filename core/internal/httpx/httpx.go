// honesty-machinery: do-not-revert — error-visibility/self-healing guard. See
// the CLAUDE.md self-healing hard rule + memory project_sentry_blind_revert.
// Auto-rollback paths (sentry, verify cron, revert-veto) MUST never revert this.

// Package httpx is the universal "Jarvis always sees his errors" capture seam.
//
// The boss's law (2026-06-24, after a Composio 401 silently zeroed inbox triage
// for days): "Jarvis MUST ALWAYS see errors he gets — no false greens that were
// really 401s, 404s, etc." The failure mode is a caller swallowing a non-2xx
// response and reporting success. We close it at the TRANSPORT, below any caller:
// an instrumented http.RoundTripper records every status >= 400 and every
// transport error into mem_http_failures (migration 153). Wrap http.DefaultTransport
// ONCE at boot (InstallDefault) and every client that uses the default transport
// is covered with zero per-vendor wiring.
//
// The recorder is strictly observational: it never alters the request, response,
// or error, never blocks the call (writes are async + best-effort, dropped on
// overflow), and writes via pgx — NOT http — so it can never recurse through the
// instrumented transport.
package httpx

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Failure is one recorded outbound-HTTP problem.
type Failure struct {
	SessionID string
	Subsystem string
	Method    string
	Host      string
	Path      string
	Status    int // 0 = transport error (no response)
	Error     string
}

// Recorder persists failures. Implementations MUST be non-blocking and
// self-contained (no outbound HTTP) so they can sit under the default transport.
type Recorder interface {
	Record(f Failure)
}

// ---- request-context session tagging -------------------------------------

type ctxKey struct{}

// WithSession tags a context with the run/session id so any HTTP failure on a
// request derived from it is attributed to that run. Deterministic crons thread
// this in their executor; the agent loop threads it per turn.
func WithSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, sessionID)
}

// SessionFrom returns the session id tagged on ctx, or "".
func SessionFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// ---- instrumented transport ----------------------------------------------

type roundTripper struct {
	base http.RoundTripper
	rec  Recorder
	name string
}

// Wrap returns a RoundTripper that records failures from base into rec. A nil
// base falls back to http.DefaultTransport; a nil rec returns base unchanged.
func Wrap(base http.RoundTripper, rec Recorder, name string) http.RoundTripper {
	if rec == nil {
		if base == nil {
			return http.DefaultTransport
		}
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base, rec: rec, name: name}
}

func (t *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	// Record transport errors (no response) and any 4xx/5xx. Never touch the
	// returned resp/err — pure observation.
	if err != nil || status >= 400 {
		f := Failure{
			SessionID: SessionFrom(req.Context()),
			Subsystem: t.name,
			Method:    req.Method,
			Status:    status,
		}
		if req.URL != nil {
			f.Host = req.URL.Host
			f.Path = req.URL.Path
		}
		if err != nil {
			f.Error = err.Error()
		}
		t.rec.Record(f)
	}
	return resp, err
}

// InstallDefault wraps http.DefaultTransport so EVERY client using the default
// transport (the common `&http.Client{Timeout: …}` with no custom Transport) is
// instrumented. Idempotent in spirit — call once at boot after the pool exists.
func InstallDefault(rec Recorder) {
	http.DefaultTransport = Wrap(http.DefaultTransport, rec, "default")
}

// ---- pgx-backed async recorder -------------------------------------------

// DBRecorder writes failures to mem_http_failures from a single background
// goroutine. Record() is non-blocking: it drops on a full buffer rather than
// ever stalling an HTTP call. Actionable() classifies which statuses count as a
// hard failure for the false-green guard.
type DBRecorder struct {
	ch chan Failure
}

// NewDBRecorder starts the writer goroutine and returns the recorder. The
// goroutine exits when ctx is cancelled.
func NewDBRecorder(ctx context.Context, pool *pgxpool.Pool) *DBRecorder {
	r := &DBRecorder{ch: make(chan Failure, 2048)}
	go r.loop(ctx, pool)
	return r
}

func (r *DBRecorder) Record(f Failure) {
	if r == nil {
		return
	}
	select {
	case r.ch <- f:
	default: // buffer full — drop rather than block the request path
	}
}

func (r *DBRecorder) loop(ctx context.Context, pool *pgxpool.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-r.ch:
			if pool == nil {
				continue
			}
			_, _ = pool.Exec(ctx, `
				INSERT INTO mem_http_failures
				  (session_id, subsystem, method, host, path, status, error)
				VALUES (NULLIF($1,''), NULLIF($2,''), $3, $4, $5, $6, NULLIF($7,''))
			`, f.SessionID, f.Subsystem, f.Method, f.Host, f.Path, f.Status, f.Error)
		}
	}
}

// IsActionableStatus reports whether a recorded status is a HARD failure that
// must veto a "green" run outcome — auth, rate-limit, timeout, and any 5xx.
// Plain 404/400 are recorded (visible) but don't auto-fail a run on their own,
// since the agent legitimately probes URLs that can 404; the guard stays high
// precision so it never cries wolf. status 0 = transport error = always hard.
func IsActionableStatus(status int) bool {
	switch {
	case status == 0:
		return true
	case status == 401 || status == 403 || status == 407 || status == 408 || status == 429:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}
