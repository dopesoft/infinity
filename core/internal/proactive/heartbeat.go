package proactive

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Heartbeat runs an isolated checklist on a tunable interval. It's fired by a
// goroutine started in cmd/infinity/serve.go.
//
// The checklist (lifted from Hal):
//   • Proactive tracker: any overdue behaviours?
//   • Pattern check: any repeated user requests to automate?
//   • Outcome check: decisions older than 7 days needing follow-up?
//   • Security: any error / injection signal in recent logs?
//   • Self-healing: errors in the run log → diagnose
//   • Memory: context %% - trigger danger zone if >60%
//   • Surprise: what could I build right now that would delight my human?
//
// Initial cut ships a *substrate*: the ticker, the persistence, and a hookable
// runner that calls a user-supplied Checklist. A future pass plugs in the curriculum
// generator + AutoSkill loop into the same pipeline.
type Heartbeat struct {
	pool      *pgxpool.Pool
	interval  time.Duration
	checklist Checklist
	clock     func() time.Time

	mu      sync.Mutex
	running bool
	stop    chan struct{}

	// onFinding fires for every finding emitted by a tick, after it's been
	// persisted. The server wires this to broadcast notable findings to any
	// active WS session as an unprompted assistant turn - the wire that
	// turns the heartbeat from a silent background job into proactive
	// behaviour. Nil-safe.
	onFinding func(context.Context, Finding)
}

// Checklist is the function the heartbeat invokes each tick. It's expected to
// run as an isolated agent turn (own session, own context window) and return
// the findings produced.
type Checklist func(ctx context.Context, h *Heartbeat) ([]Finding, error)

// Finding represents a single diagnostic / suggestion / surprise produced by
// the heartbeat.
type Finding struct {
	Kind        string `json:"kind"` // pattern | outcome | curiosity | surprise | security | self_heal
	Title       string `json:"title"`
	Detail      string `json:"detail,omitempty"`
	PreApproved bool   `json:"pre_approved"`
	// Source is the finer-grained origin within a Kind - e.g. a curiosity
	// finding's Source is "high_surprise" | "contradiction" |
	// "uncovered_mention". Used by the chat formatter to explain *why* the
	// finding surfaced; not persisted (mem_heartbeat_findings has no column
	// for it), purely an in-process hint.
	Source string `json:"source,omitempty"`
	// CuriosityID links a curiosity finding back to its row in
	// mem_curiosity_questions so the chat surface can offer an
	// "Approve & fix" action that marks the question and lets the agent
	// act on it. Empty for findings with no curiosity-question backing.
	CuriosityID string `json:"curiosity_id,omitempty"`
	// SourceTag is the stable identifier for "what condition this is
	// about" - e.g. 'connector_identity_resolution',
	// 'cron_failure:<id>', 'repeated_tool_error:<tool>'. Persisting
	// the tag lets the heartbeat auto-resolve previous findings when
	// a fresh one for the same condition lands (titles can vary -
	// "4 accounts" -> "2 accounts" should NOT pile up - but the tag
	// is stable). Optional; when empty, no auto-supersede happens.
	SourceTag string `json:"source_tag,omitempty"`
}

func NewHeartbeat(p *pgxpool.Pool, interval time.Duration, checklist Checklist) *Heartbeat {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &Heartbeat{
		pool:      p,
		interval:  interval,
		checklist: checklist,
		clock:     time.Now,
		stop:      make(chan struct{}),
	}
}

func (h *Heartbeat) Interval() time.Duration { return h.interval }

// SetOnFinding registers a callback invoked once per finding after the tick
// persists it. Safe to call before Start.
func (h *Heartbeat) SetOnFinding(fn func(context.Context, Finding)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.onFinding = fn
	h.mu.Unlock()
}

// callback reads the on-finding callback under the lock so concurrent
// SetOnFinding calls don't race the tick goroutine.
func (h *Heartbeat) callback() func(context.Context, Finding) {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	cb := h.onFinding
	h.mu.Unlock()
	return cb
}

// Start kicks off the ticker goroutine. Safe to call once. Use Stop to halt.
func (h *Heartbeat) Start(ctx context.Context) {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()

	go h.loop(ctx)
}

func (h *Heartbeat) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running {
		return
	}
	close(h.stop)
	h.running = false
}

func (h *Heartbeat) loop(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stop:
			return
		case <-ticker.C:
			h.RunOnce(ctx)
		}
	}
}

// RunOnce executes a single tick synchronously. Useful for the "Run heartbeat
// now" button in Studio.
func (h *Heartbeat) RunOnce(ctx context.Context) (RunSummary, error) {
	if h == nil || h.checklist == nil {
		return RunSummary{}, nil
	}
	start := h.clock()
	hbID, _ := h.startRun(ctx, start)

	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	findings, err := h.checklist(cctx, h)
	end := h.clock()
	dur := end.Sub(start)

	status := "ok"
	if err != nil {
		status = "error"
	}

	summary := summariseFindings(findings)

	if hbID != "" && h.pool != nil {
		_, _ = h.pool.Exec(ctx, `
			UPDATE mem_heartbeats
			   SET ended_at = $2, duration_ms = $3, findings = $4, summary = $5, status = $6
			 WHERE id = $1::uuid
		`, hbID, end, dur.Milliseconds(), len(findings), summary, status)
		for _, f := range findings {
			// Auto-supersede any earlier open finding with the same
			// source_tag - that's the count-varying case
			// ("4 accounts" -> "2 accounts" finding pair). Schema's
			// migration 034 added the status column; older rows that
			// were backfilled to 'resolved' aren't touched.
			if f.SourceTag != "" {
				_, _ = h.pool.Exec(ctx, `
					UPDATE mem_heartbeat_findings
					   SET status = 'resolved', resolved_at = NOW()
					 WHERE source_tag = $1 AND status = 'open'
				`, f.SourceTag)
			}
			_, _ = h.pool.Exec(ctx, `
				INSERT INTO mem_heartbeat_findings
				  (heartbeat_id, kind, title, detail, pre_approved, source_tag)
				VALUES ($1::uuid, $2, $3, $4, $5, $6)
			`, hbID, f.Kind, f.Title, f.Detail, f.PreApproved, f.SourceTag)
			if cb := h.callback(); cb != nil {
				cb(ctx, f)
			}
		}
	}

	return RunSummary{
		ID:         hbID,
		StartedAt:  start,
		EndedAt:    end,
		DurationMS: dur.Milliseconds(),
		Findings:   findings,
		Status:     status,
		Error:      stringErr(err),
	}, err
}

// ResolveSourceTag marks every open finding with the given source_tag
// as resolved. Checklists call this when their condition has cleared
// but they have NO new finding to emit - e.g. ConnectorIdentityChecklist
// sees missing=0, so any earlier "N accounts need identity" finding
// should be closed even though no new row is being written this tick.
// No-op for empty tag, nil pool, or no matching open rows.
func ResolveSourceTag(ctx context.Context, pool *pgxpool.Pool, tag string) {
	if pool == nil || tag == "" {
		return
	}
	_, _ = pool.Exec(ctx, `
		UPDATE mem_heartbeat_findings
		   SET status = 'resolved', resolved_at = NOW()
		 WHERE source_tag = $1 AND status = 'open'
	`, tag)
}

// RunSummary is the result returned from RunOnce. Suitable for a JSON payload
// over /api/heartbeat/run.
type RunSummary struct {
	ID         string    `json:"id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMS int64     `json:"duration_ms"`
	Findings   []Finding `json:"findings"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

func (h *Heartbeat) startRun(ctx context.Context, started time.Time) (string, error) {
	if h.pool == nil {
		return "", nil
	}
	var id string
	err := h.pool.QueryRow(ctx, `
		INSERT INTO mem_heartbeats (started_at, status)
		VALUES ($1, 'running') RETURNING id::text
	`, started).Scan(&id)
	return id, err
}

func summariseFindings(fs []Finding) string {
	if len(fs) == 0 {
		return "no findings"
	}
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- [%s] %s", f.Kind, f.Title)
	}
	return b.String()
}

func stringErr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
