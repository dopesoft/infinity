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
	"strings"
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
	// KindSkillPromote is a candidate-skill promotion: it RUNS the skill's
	// verification harness (an ephemeral LLM session, up to ~90s) before
	// promoting, so the decide endpoint books this and works in the background
	// while the Studio card shows a live spinner. target_id is the proposal id.
	KindSkillPromote Kind = "skill.promote"
	KindHeartbeat       Kind = "heartbeat"
	KindVoyagerOptimize Kind = "voyager.optimize"
	KindVoyagerExtract  Kind = "voyager.extract"
	KindGymExtract      Kind = "gym.extract"
	KindSentinel        Kind = "sentinel"
	KindBrowserSession  Kind = "browser.session"
	KindExtension       Kind = "extension.register"
	KindBackgroundBuild Kind = "background.build"
	KindSurfaceAction   Kind = "surface.action"
	KindPlanStep        Kind = "plan.step"
	// KindMediaGenerate is a media-producing job (image/video/graphic render)
	// run via the generic media_job tool. target_id is the session id so the
	// Studio Media tab can filter media.generate runs to the current session,
	// and the produced assets ride mem_runs.meta.media for live display.
	KindMediaGenerate Kind = "media.generate"
	// KindPhoneCall is a live SIP call (inbound or outbound) monitored by
	// the phone substrate. target_id is the OpenAI realtime call id; the row
	// is live for the whole call so Studio shows an "on a call" spinner that
	// survives navigation.
	KindPhoneCall Kind = "phone.call"
	// KindPhoneAsk is a boss-typed call errand from the dashboard header
	// ("call Sanson's and order a pepperoni"): the background agent turn
	// that finds the number, writes the brief, and places the call. The
	// resulting call itself is a separate KindPhoneCall row.
	KindPhoneAsk Kind = "phone.ask"
	// KindAttachmentIngest is the read of a file the boss attached in chat:
	// mirror to the workspace, extract text, rasterize scanned pages. Booked so
	// the /logs Runs lens shows why an upload took a moment and what it found.
	KindAttachmentIngest Kind = "attachment.ingest"
	// KindCodeAgent is an inline `claude -p` coding job launched by the
	// code_agent tool on the Mac bridge. Booked with a literal string there
	// (tools/code_agent.go); the constant exists so the sweeps below and the
	// cron reconciler name the same kind rather than three loose literals.
	KindCodeAgent Kind = "code_agent"
)

// Stopped reasons stamped into mem_runs.meta.stopped_reason by
// FinishInterrupted and the boot sweep. Their whole job is to make
// "this run never reached a verdict" DISTINGUISHABLE from "this run failed",
// so no downstream reader (plan.Store.ReconcileStranded above all) can launder
// an interruption into a red ❌ on the boss's board.
//
// The set is deliberately small and additive: mem_runs.status keeps its
// existing CHECK ('running' | 'ok' | 'error') so every existing reader — Go and
// studio — is untouched. A row with a stopped_reason is a row that closed
// WITHOUT a verdict; its result_summary always says so in plain English.
const (
	// StoppedInterrupted: the process running it went away (deploy SIGTERM,
	// crash, OOM) before it could record a result. Nothing failed; nothing was
	// rolled back; we simply have no receipt.
	StoppedInterrupted = "interrupted"
	// StoppedStillWorking: the inline wait window closed while the worker kept
	// going (code_agent / background_build's stillRunningError). The job was NOT
	// killed — this row stops tracking, the work continues.
	StoppedStillWorking = "still_working"
)

// codingKinds are the DETACHED coding jobs: they outlive the turn, the chat
// session, and (via nohup reparenting on the Mac) the core process itself, so
// they manage their own lifecycle. Two rules key off this list:
//
//  1. the blanket 45-min ReapTimedOut skips them — a real coding job
//     legitimately runs longer than any wall clock we'd pick; and
//  2. RecoverStranded closes them as interrupted-not-failed — a job that
//     outlived a core restart is still on disk on the Mac, so calling it
//     "failed" is a lie the boss sees as a red step and a "Build failed" push.
//
// They are still CLOSED honestly: cron.ReconcileReaped re-derives an outcome
// for them from recorded signal on a long grace, so exempting them here never
// means "spins forever".
var codingKinds = []Kind{KindBackgroundBuild, KindCodeAgent}

// CodingKinds returns the detached-coding kinds as plain strings, for SQL
// parameters and for callers outside this package that need the same list.
func CodingKinds() []string {
	out := make([]string, 0, len(codingKinds))
	for _, k := range codingKinds {
		out = append(out, string(k))
	}
	return out
}

// IsCodingKind reports whether a mem_runs kind is a detached coding job.
func IsCodingKind(kind string) bool {
	for _, k := range codingKinds {
		if string(k) == kind {
			return true
		}
	}
	return false
}

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
	pushMsg := "" // boss-facing text for the push; human title on failure
	if err != nil {
		status = "error"
		errStr = err.Error()
		// Translate the raw error into a boss-facing explanation alongside the
		// raw string, so the UI can show "your model token was revoked" instead
		// of the provider's JSON. Best-effort: a marshal failure just leaves
		// human_error empty, never blocks the row close.
		human := errs.HumanizeString(errStr)
		pushMsg = human.Title
		if b, mErr := json.Marshal(human); mErr == nil {
			humanJSON = string(b)
		}
	}
	// Never close a successful run with a blank narrative — the dashboard would
	// render an explanation-less card that reads as "did it even run?". A
	// guaranteed floor keeps every Done card legible (callers that have a real
	// summary still win; this only fills the gap).
	if status == "ok" && strings.TrimSpace(summary) == "" {
		summary = "Completed — nothing to report."
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
		// On failure, hand the notifier the humanized title (not the raw
		// provider JSON) so the push reads like a thought, not a stack trace.
		msg := errStr
		if pushMsg != "" {
			msg = pushMsg
		}
		go notifier.NotifyRunFinished(context.Background(), h.id, h.kind, h.label, status, msg, end.Sub(h.start))
	}
	// Clear id so a second Finish call no-ops.
	h.id = ""
}

// FinishInterrupted closes a run that STOPPED WITHOUT A VERDICT — it was
// neither a success nor a failure. Use it for exactly two situations:
//
//   - StoppedStillWorking: the inline wait window closed and the worker kept
//     going (code_agent / background_build's stillRunningError, whose own doc
//     says "The job was NOT killed").
//   - StoppedInterrupted: the process was taken away mid-run (deploy, crash).
//
// Contract:
//
//	status         = 'ok'   (NOT a new enum value — mem_runs' CHECK constraint
//	                        is ('running','ok','error') and every reader, Go and
//	                        studio, already understands those three)
//	error          = ''      — there was no error; nothing to humanize
//	human_error    = '{}'    — ditto, so no failure card renders
//	result_summary = an honest, first-person line saying it did NOT finish
//	meta.stopped_reason = reason  — the machine-readable discriminator
//
// The stopped_reason is what makes this un-launderable: plan.Store's
// ReconcileStranded fails a step only for a run that is 'error' AND carries NO
// stopped_reason, so an interrupted run can never be read as "unambiguous proof
// the execution finished and failed".
//
// It deliberately does NOT fire the push notifier. A run that hasn't reached a
// verdict has nothing to announce; pushing "done" would be the exact false
// green this whole path exists to prevent. Studio still updates live off the
// realtime mem_runs row.
//
// This NEVER hides a real failure: it is only reachable from paths that have
// positive proof the work was interrupted or is still going. Any error at all
// still goes through Finish → status 'error' → red, surfaced, backlogged.
//
// nil-safe; safe to call twice (the second call no-ops).
func (h *Handle) FinishInterrupted(ctx context.Context, reason, summary string) {
	if h == nil || h.tracker == nil || h.tracker.pool == nil || h.id == "" {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = StoppedInterrupted
	}
	summary = InterruptedSummary(reason, summary)
	patch, err := json.Marshal(map[string]any{"stopped_reason": reason})
	if err != nil {
		patch = []byte(`{"stopped_reason":"` + StoppedInterrupted + `"}`)
	}
	end := time.Now().UTC()
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = 'ok',
		       ended_at = $2,
		       duration_ms = $3,
		       error = '',
		       result_summary = $4,
		       human_error = '{}'::jsonb,
		       meta = COALESCE(meta,'{}'::jsonb) || $5::jsonb
		 WHERE id = $1::uuid
	`, h.id, end, end.Sub(h.start).Milliseconds(), summary, string(patch))
	// Clear id so a second Finish/FinishInterrupted call no-ops.
	h.id = ""
}

// InterruptedSummary is the guaranteed first-person narrative for a run that
// closed without a verdict. A caller's own text wins when it has one; this is
// the floor that stops a no-verdict run from rendering as a wordless green card
// the boss reads as success (CLAUDE.md → "Failures must read human" +
// "Explain value simply").
func InterruptedSummary(reason, own string) string {
	if s := strings.TrimSpace(own); s != "" {
		return s
	}
	if reason == StoppedStillWorking {
		return "Still working on this. The window I was watching it through closed, not the job, " +
			"so nothing was stopped and nothing was lost. I'll report the result once it lands."
	}
	return "I got cut off part-way through this, so I never wrote up a result. " +
		"Nothing broke and nothing was rolled back: what I'd already done is still there. " +
		"I simply don't have a verdict for you on this one."
}

// RecoverStranded closes every mem_runs row still marked 'running' at boot.
// A 'running' row was booked by Begin in a previous process that has since
// died (deploy, crash, OOM); the in-process Handle that would have called
// Finish is gone, so without this sweep the row spins forever in the UI (the
// exact symptom: a nightly-cognition run stuck 'running' for hours). Core is
// a single Railway instance, so any 'running' row visible at startup is
// definitively orphaned. Mirrors memory.TurnStore.RecoverStranded; call it
// once at boot. Returns the number of rows closed.
//
// INTERRUPTED IS NOT FAILED. Every row this sweep touches was interrupted by
// definition — the process was taken away, the work itself never returned a
// verdict — so every row it closes is stamped meta.stopped_reason='interrupted'
// (recoverStrandedReasonPatch). That stamp is the discriminator downstream
// readers key off: plan.Store.ReconcileStranded fails a step only for a run
// that errored AND carries no stopped_reason, so a restart can no longer
// launder itself into a red ❌ step and a "Build failed" push.
//
// Two lanes, because the honest STATUS differs by kind:
//
//   - detached coding jobs (CodingKinds) close 'ok' + interrupted. A Mac
//     `claude -p` is nohup-reparented: it very likely outlived the restart and
//     is still editing files. Calling that "failed" is simply false.
//   - everything else keeps the existing 'error' close, unchanged, so nothing
//     that used to go red stops going red. Only its reason is now recorded.
//
// Both lanes preserve the last progress checkpoint in the summary.
func (t *Tracker) RecoverStranded(ctx context.Context) (int, error) {
	if t == nil || t.pool == nil {
		return 0, nil
	}
	coding, err := t.pool.Exec(ctx, recoverStrandedCodingSQL, CodingKinds())
	if err != nil {
		return 0, fmt.Errorf("recover stranded coding runs: %w", err)
	}
	rest, err := t.pool.Exec(ctx, recoverStrandedSQL, CodingKinds())
	if err != nil {
		return int(coding.RowsAffected()), fmt.Errorf("recover stranded runs: %w", err)
	}
	return int(coding.RowsAffected() + rest.RowsAffected()), nil
}

// recoverStrandedReasonPatch is the meta merge both boot lanes apply. Kept as
// one const so the two statements can never drift apart on the one field every
// downstream honesty check reads.
const recoverStrandedReasonPatch = `meta = COALESCE(meta,'{}'::jsonb) || '{"stopped_reason":"` + StoppedInterrupted + `"}'::jsonb`

// recoverStrandedDurationSQL is the shared "how long had it been going?" clamp.
const recoverStrandedDurationSQL = `duration_ms = COALESCE(duration_ms,
	    LEAST(2147483647, GREATEST(0,
	        EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int)`

// recoverStrandedCodingSQL closes detached coding jobs orphaned by a restart.
// status='ok' + stopped_reason='interrupted': not a success claim (the summary
// says plainly there is no verdict), just a refusal to call an interruption a
// failure. $1 is the coding-kind list.
const recoverStrandedCodingSQL = `
	UPDATE mem_runs
	   SET status = 'ok',
	       ended_at = COALESCE(ended_at, NOW()),
	       ` + recoverStrandedDurationSQL + `,
	       error = '',
	       human_error = '{}'::jsonb,
	       result_summary = COALESCE(
	           NULLIF(result_summary, ''),
	           CASE
	               WHEN COALESCE(progress_label,'') <> ''
	                 THEN 'I lost track of this when the box restarted. It was not stopped or rolled back, I just have no result for it. Last checkpoint: ' || progress_label
	               ELSE 'I lost track of this when the box restarted. It was not stopped or rolled back, I just have no result for it.'
	           END
	       ),
	       ` + recoverStrandedReasonPatch + `
	 WHERE status = 'running'
	   AND kind = ANY($1::text[])`

// recoverStrandedSQL closes every other orphaned run. The 'error' close is
// unchanged from before this file learned about interruptions — only the
// stopped_reason stamp is new, and it exists so the plan layer can tell a
// restart apart from a genuine execution failure.
const recoverStrandedSQL = `
	UPDATE mem_runs
	   SET status = 'error',
	       ended_at = COALESCE(ended_at, NOW()),
	       ` + recoverStrandedDurationSQL + `,
	       error = COALESCE(NULLIF(error, ''), 'core restarted while run was in flight'),
	       result_summary = COALESCE(
	           NULLIF(result_summary, ''),
	           CASE
	               WHEN COALESCE(progress_label,'') <> '' THEN 'Interrupted mid-run. Last checkpoint: ' || progress_label
	               ELSE '(interrupted by restart)'
	           END
	       ),
	       ` + recoverStrandedReasonPatch + `
	 WHERE status = 'running'
	   AND NOT (kind = ANY($1::text[]))`

// ReapTimedOut closes mem_runs still 'running' past maxAge — a run whose owning
// turn or process ended without closing it (the agent gave up mid-turn, the box
// restarted, a detached job hung). Without this a stale row spins forever on the
// Agent Work board (the exact symptom: an inbox-triage plan.step stuck 'running'
// for 6 hours after the turn had already TaskCompleted). Unlike RecoverStranded
// (boot-only, closes ALL running rows), this is age-bounded so it's SAFE to run
// on a live process — a run younger than maxAge is presumed still legitimately
// working. CodingKinds (background.build AND code_agent) are excluded: those are
// long-lived detached jobs that manage their own lifecycle, and a wall clock
// cannot tell a 50-minute build apart from a dead one. code_agent joined the
// exemption because it was the laundering everyone saw: a `claude -p` still
// editing files on the Mac got stamped 'error' at 45 minutes, which
// plan.Store.ReconcileStranded then read as proof the step had failed. Those
// runs are still closed honestly — cron.ReconcileReaped re-derives an outcome
// for them from recorded signal on a long grace, so exemption never means
// "spins forever". The boss-facing human_error is set so the card reads
// "(stalled)" rather than going silent. Returns rows closed.
//
// Note the asymmetry with the interrupted path above: a reaped run gets NO
// stopped_reason. Blowing past a time budget with nothing recorded IS a
// failure — it stays red, stays surfaced, and still fails its plan step.
func (t *Tracker) ReapTimedOut(ctx context.Context, maxAge time.Duration) (int, error) {
	if t == nil || t.pool == nil {
		return 0, nil
	}
	if maxAge <= 0 {
		maxAge = 45 * time.Minute
	}
	humanJSON := "{}"
	if b, err := json.Marshal(errs.HumanizeString("the run ran past its time budget and was stopped")); err == nil {
		humanJSON = string(b)
	}
	tag, err := t.pool.Exec(ctx, reapTimedOutSQL, maxAge.Seconds(), humanJSON, CodingKinds())
	if err != nil {
		return 0, fmt.Errorf("reap timed-out runs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// reapTimedOutSQL is ReapTimedOut's statement. $3 is the coding-kind list that
// is exempt from the blanket clock — see the doc on ReapTimedOut.
const reapTimedOutSQL = `
	UPDATE mem_runs
	   SET status = 'error',
	       ended_at = COALESCE(ended_at, NOW()),
	       duration_ms = COALESCE(duration_ms,
	           LEAST(2147483647, GREATEST(0,
	               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
	       error = COALESCE(NULLIF(error, ''), 'run exceeded its time budget and was reaped'),
	       result_summary = COALESCE(NULLIF(result_summary, ''), '(stalled — no result recorded)'),
	       human_error = CASE
	           WHEN human_error IS NULL OR human_error = '{}'::jsonb THEN $2::jsonb
	           ELSE human_error
	       END
	 WHERE status = 'running'
	   AND NOT (kind = ANY($3::text[]))
	   AND started_at < NOW() - ($1 * interval '1 second')`

// ReapTimedOutKind is ReapTimedOut scoped to a single kind, so a class of run
// with a tighter SLA than the global 45-min budget can be reaped faster. Used
// for 'plan.step' spinners: a step stranded by a crashed turn (the OnDone settle
// is the normal close path) should surface within minutes, not 45.
//
// TURN-CLOSED GATE (not just age): a plan.step run whose owning session STILL
// has an in-flight turn is NOT reaped, no matter how old it is. Age alone is a
// lie here — a legitimate multi-minute (even 20-minute) hosted web_search inside
// a live turn kept getting FALSELY reaped as "stalled — no result recorded",
// which cascaded into ReconcileStranded failing the step and pausing the whole
// plan (the 2026-07-02 "Research the market ❌ while the turn was still working"
// incident). mem_runs has no session_id, so we join through the plan graph
// (run → mem_plan_steps.run_id → mem_plans → mem_turns on session_id) and only
// reap when NO turn on that session is 'in_flight'. A run with no plan-step
// backing (any other kind) doesn't match the join, so NOT EXISTS is trivially
// true and the reap proceeds exactly as before — backward compatible.
// Returns rows closed.
func (t *Tracker) ReapTimedOutKind(ctx context.Context, kind Kind, maxAge time.Duration) (int, error) {
	if t == nil || t.pool == nil {
		return 0, nil
	}
	if maxAge <= 0 {
		maxAge = 10 * time.Minute
	}
	humanJSON := "{}"
	if b, err := json.Marshal(errs.HumanizeString("the step ran past its time budget and was stopped")); err == nil {
		humanJSON = string(b)
	}
	tag, err := t.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = 'error',
		       ended_at = COALESCE(ended_at, NOW()),
		       duration_ms = COALESCE(duration_ms,
		           LEAST(2147483647, GREATEST(0,
		               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
		       error = COALESCE(NULLIF(error, ''), 'step exceeded its time budget and was reaped'),
		       result_summary = COALESCE(NULLIF(result_summary, ''), '(stalled — no result recorded)'),
		       human_error = CASE
		           WHEN human_error IS NULL OR human_error = '{}'::jsonb THEN $2::jsonb
		           ELSE human_error
		       END
		 WHERE status = 'running'
		   AND kind = $3
		   AND started_at < NOW() - ($1 * interval '1 second')
		   AND NOT EXISTS (
		       SELECT 1
		         FROM mem_plan_steps st
		         JOIN mem_plans p ON p.id = st.plan_id
		         JOIN mem_turns tn ON tn.session_id = p.session_id
		        WHERE st.run_id = mem_runs.id
		          AND tn.status = 'in_flight'
		   )
	`, maxAge.Seconds(), humanJSON, string(kind))
	if err != nil {
		return 0, fmt.Errorf("reap timed-out %s runs: %w", kind, err)
	}
	return int(tag.RowsAffected()), nil
}

// FinishByID closes a run row by its id, without needing the original Handle.
// Used when begin and finish span different turns / tool calls (eg. a plan
// step booked 'running' by plan_update on one turn and closed on a later turn),
// where the in-memory Handle from BeginGlobal can't be held. err==nil ->
// status='ok'; err!=nil -> status='error'. summary is the optional human
// narrative. nil-safe; a row already closed is left untouched.
func FinishByID(ctx context.Context, runID string, err error, summary string) {
	if global == nil || global.pool == nil || runID == "" {
		return
	}
	status := "ok"
	errStr := ""
	humanJSON := "{}"
	if err != nil {
		status = "error"
		errStr = err.Error()
		if b, mErr := json.Marshal(errs.HumanizeString(errStr)); mErr == nil {
			humanJSON = string(b)
		}
	}
	_, _ = global.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET status = $2,
		       ended_at = NOW(),
		       duration_ms = COALESCE(duration_ms,
		           LEAST(2147483647, GREATEST(0,
		               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
		       error = $3,
		       result_summary = CASE WHEN $4 = '' THEN result_summary ELSE $4 END,
		       human_error = $5::jsonb
		 WHERE id = $1::uuid AND status = 'running'
	`, runID, status, errStr, summary, humanJSON)
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

// SetMeta shallow-merges the given key/values into mem_runs.meta, preserving
// any keys already there (jsonb `||`). Used by the cron scheduler to stash an
// executor's structured RunSummary.Meta (turn/tool counts, session id, the
// nightly cognition report) so the run detail can render it. Best-effort +
// nil-safe; an empty/nil map is a no-op.
func (h *Handle) SetMeta(ctx context.Context, meta map[string]any) {
	if h == nil || h.tracker == nil || h.tracker.pool == nil || h.id == "" || len(meta) == 0 {
		return
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return
	}
	_, _ = h.tracker.pool.Exec(ctx, `
		UPDATE mem_runs
		   SET meta = COALESCE(meta,'{}'::jsonb) || $2::jsonb
		 WHERE id = $1::uuid
	`, h.id, string(b))
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
