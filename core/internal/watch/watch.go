// Package watch is the deterministic "watch a long action until it settles,
// then report back into the chat" substrate. It is the generic building block
// behind the `watch_until` agent tool.
//
// The contract (mem_watches, migration 086) is intentionally tiny and generic:
// a watch points at a mem_runs row (kind="run") or a cron (kind="cron") and
// carries the session to deliver the follow-up into. A single Go Poller ticks
// over due, still-pending watches, resolves their target's status against
// mem_runs — pure code, no LLM cognition — and the instant the run leaves
// 'running' (or the watch times out) it fires ONE proactive message into the
// originating session via the injected Notifier, then closes the row.
//
// This is the supported replacement for the anti-pattern that prompted it: a
// fire-and-forget `claude_code__Bash` poll on the Mac, whose completion can
// never wake the agent loop and so can never deliver the promised callback.
package watch

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog routes success lines to stdout so Railway tags them severity:info
// instead of the stderr default (severity:error). See CLAUDE.md → "Logging".
var infoLog = log.New(os.Stdout, "watch: ", log.LstdFlags)

// Notifier delivers a settled watch's follow-up message into a session. The
// serve.go adapter fans this out to the live WS (proactive_message) AND the
// push sender, so the boss learns whether a tab is open or not. Best-effort:
// a slow or failing notification must never wedge the poller.
type Notifier interface {
	Notify(sessionID, text string)
}

// Watch is one row of mem_watches in flight.
type Watch struct {
	SessionID   string
	Kind        string // "run" | "cron"
	TargetID    string // mem_runs.id (kind=run) or cron id (kind=cron)
	TargetLabel string
	Note        string
	PollSeconds int
	Timeout     time.Duration
}

// Store is the create + lifecycle handle over mem_watches.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a Store, or nil when no pool is configured (the tool then
// no-ops gracefully so chat-only / migrate-only runs don't break).
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		return nil
	}
	return &Store{pool: pool}
}

// Create inserts a pending watch and returns its id. Defaults are applied for
// poll cadence and timeout so callers can pass a zero-value of either.
func (s *Store) Create(ctx context.Context, w Watch) (string, error) {
	if s == nil || s.pool == nil {
		return "", fmt.Errorf("watch store not configured")
	}
	w.Kind = strings.TrimSpace(w.Kind)
	if w.Kind != "run" && w.Kind != "cron" {
		w.Kind = "run"
	}
	if strings.TrimSpace(w.SessionID) == "" {
		return "", fmt.Errorf("watch needs a session id")
	}
	if strings.TrimSpace(w.TargetID) == "" {
		return "", fmt.Errorf("watch needs a target id")
	}
	if w.PollSeconds <= 0 {
		w.PollSeconds = 8
	}
	if w.Timeout <= 0 {
		w.Timeout = 30 * time.Minute
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_watches
		  (session_id, kind, target_id, target_label, note, poll_seconds, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW() + ($7 || ' seconds')::interval)
		RETURNING id::text
	`, w.SessionID, w.Kind, w.TargetID, w.TargetLabel, w.Note, w.PollSeconds,
		fmt.Sprintf("%d", int(w.Timeout.Seconds()))).Scan(&id)
	if err != nil {
		return "", err
	}
	infoLog.Printf("created watch %s kind=%s target=%s session=%s", id, w.Kind, w.TargetID, w.SessionID)
	return id, nil
}

// Poller is the deterministic background loop that settles watches.
type Poller struct {
	pool     *pgxpool.Pool
	notifier Notifier
	interval time.Duration
}

// NewPoller returns a Poller, or nil when its dependencies are missing.
func NewPoller(pool *pgxpool.Pool, n Notifier) *Poller {
	if pool == nil || n == nil {
		return nil
	}
	return &Poller{pool: pool, notifier: n, interval: 3 * time.Second}
}

// Start runs the tick loop until ctx is cancelled. Pending watches are durable
// in mem_watches, so a process restart resumes them automatically on the next
// tick — no special recovery path needed.
func (p *Poller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	infoLog.Printf("poller started (interval=%s)", p.interval)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

type dueWatch struct {
	id          string
	sessionID   string
	kind        string
	targetID    string
	label       string
	note        string
	pollSeconds int
	createdAt   time.Time
	expiresAt   time.Time
}

func (p *Poller) tick(ctx context.Context) {
	rows, err := p.pool.Query(ctx, `
		SELECT id::text, session_id::text, kind, target_id,
		       target_label, note, poll_seconds, created_at, expires_at
		  FROM mem_watches
		 WHERE status = 'pending' AND next_poll_at <= NOW()
		 ORDER BY next_poll_at
		 LIMIT 50
	`)
	if err != nil {
		log.Printf("watch: tick query: %v", err)
		return
	}
	var due []dueWatch
	for rows.Next() {
		var d dueWatch
		if err := rows.Scan(&d.id, &d.sessionID, &d.kind, &d.targetID,
			&d.label, &d.note, &d.pollSeconds, &d.createdAt, &d.expiresAt); err != nil {
			log.Printf("watch: scan: %v", err)
			continue
		}
		due = append(due, d)
	}
	rows.Close()

	for _, d := range due {
		p.process(ctx, d)
	}
}

func (p *Poller) process(ctx context.Context, d dueWatch) {
	// Timed out before the target settled — close it honestly rather than
	// leaving the boss hanging.
	if time.Now().After(d.expiresAt) {
		p.settle(ctx, d, "timeout", "")
		return
	}
	status, errMsg, durMs, found := p.resolve(ctx, d)
	if !found || status == "running" {
		// Not terminal yet: schedule the next poll.
		_, _ = p.pool.Exec(ctx, `
			UPDATE mem_watches
			   SET next_poll_at = NOW() + ($2 || ' seconds')::interval,
			       last_state = $3
			 WHERE id = $1::uuid
		`, d.id, fmt.Sprintf("%d", d.pollSeconds), status)
		return
	}
	p.settle(ctx, d, status, p.outcomeDetail(status, errMsg, durMs))
}

// resolve reads the watched target's current status from mem_runs. For a cron
// it grabs the most recent run that started within a short lookback of the
// watch's creation, which fits "I just fired/will-fire it, watch THIS run"
// whether the fire was synchronous (already terminal) or async (still running).
func (p *Poller) resolve(ctx context.Context, d dueWatch) (status, errMsg string, durMs int, found bool) {
	var (
		st  string
		em  string
		dms *int
	)
	switch d.kind {
	case "cron":
		err := p.pool.QueryRow(ctx, `
			SELECT status, error, duration_ms
			  FROM mem_runs
			 WHERE kind = 'cron' AND target_id = $1
			   AND started_at >= $2::timestamptz - INTERVAL '10 minutes'
			 ORDER BY started_at DESC
			 LIMIT 1
		`, d.targetID, d.createdAt).Scan(&st, &em, &dms)
		if err != nil {
			return "", "", 0, false
		}
	default: // "run"
		err := p.pool.QueryRow(ctx, `
			SELECT status, error, duration_ms
			  FROM mem_runs
			 WHERE id = $1::uuid
			 LIMIT 1
		`, d.targetID).Scan(&st, &em, &dms)
		if err != nil {
			return "", "", 0, false
		}
	}
	if dms != nil {
		durMs = *dms
	}
	return st, em, durMs, true
}

func (p *Poller) outcomeDetail(status, errMsg string, durMs int) string {
	if status == "error" {
		if strings.TrimSpace(errMsg) != "" {
			return errMsg
		}
		return "failed (no error message recorded)"
	}
	if durMs > 0 {
		return "in " + humanizeMs(durMs)
	}
	return ""
}

// settle closes the watch row and fires the single follow-up message.
func (p *Poller) settle(ctx context.Context, d dueWatch, outcome, detail string) {
	newStatus := "settled"
	if outcome == "timeout" {
		newStatus = "expired"
	}
	if _, err := p.pool.Exec(ctx, `
		UPDATE mem_watches
		   SET status = $2, outcome = $3, last_state = $3, settled_at = NOW()
		 WHERE id = $1::uuid AND status = 'pending'
	`, d.id, newStatus, outcome); err != nil {
		log.Printf("watch: settle %s: %v", d.id, err)
		return
	}
	text := formatReport(d.label, outcome, detail, d.note)
	infoLog.Printf("settled watch %s outcome=%s session=%s", d.id, outcome, d.sessionID)
	p.notifier.Notify(d.sessionID, text)
}

// formatReport builds the chat message the boss sees when a watch settles.
func formatReport(label, outcome, detail, note string) string {
	if strings.TrimSpace(label) == "" {
		label = "the watched job"
	}
	var b strings.Builder
	switch outcome {
	case "ok":
		b.WriteString("✅ **" + label + "** finished — **ok**")
		if detail != "" {
			b.WriteString(" (" + detail + ")")
		}
		b.WriteString(".")
	case "error":
		b.WriteString("❌ **" + label + "** failed.")
		if detail != "" {
			b.WriteString("\n\n```\n" + clip(detail, 600) + "\n```")
		}
	case "timeout":
		b.WriteString("⏱️ Still watching **" + label + "** — it hasn't reached a terminal status within the watch window, so I'm standing down. Re-run it or check `/logs` if you expected it to finish.")
	default:
		b.WriteString("**" + label + "** settled: " + outcome)
	}
	if strings.TrimSpace(note) != "" && outcome != "timeout" {
		b.WriteString("\n\n_" + note + "_")
	}
	return b.String()
}

func humanizeMs(ms int) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
