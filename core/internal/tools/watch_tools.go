package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/watch"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WatchCreator is the tiny seam watch_until depends on so the tools package
// doesn't import the watch poller wholesale (mirrors how cron tools depend on
// CronScheduler). watch.Store satisfies it.
type WatchCreator interface {
	Create(ctx context.Context, w watch.Watch) (string, error)
}

// RegisterWatchTools wires the watch_until tool. No-op when the store/pool
// isn't configured so chat-only / no-DB deployments don't break registration.
func RegisterWatchTools(r *Registry, store WatchCreator, pool *pgxpool.Pool) {
	if r == nil || store == nil {
		return
	}
	r.Register(&watchUntil{store: store, pool: pool})
}

// ── watch_until ─────────────────────────────────────────────────────────────

type watchUntil struct {
	store WatchCreator
	pool  *pgxpool.Pool
}

func (t *watchUntil) Name() string { return "watch_until" }

func (t *watchUntil) Description() string {
	return "Watch a long-running action until it reaches a terminal status (ok or error), then deliver ONE follow-up message into THIS chat — even after this turn ends. This is the deterministic, durable way to keep an 'I'll let you know how it goes' promise: a Go poller checks the real status in mem_runs and reports back; it survives turn end, restarts, and the boss closing the tab (it also pushes his phone). Use it for async work you committed to report on — a cron run, a background job, a deploy. Provide a run_id (any tool that returned one) OR a cron name/id. NEVER fake a watcher with a backgrounded bash poll; that can't call you back."
}

func (t *watchUntil) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id":          map[string]any{"type": "string", "description": "The mem_runs id to watch (preferred — zero ambiguity). Use the run_id a tool handed back."},
			"cron":            map[string]any{"type": "string", "description": "A cron name or id to watch instead of a run_id; resolves to that cron's most recent run."},
			"label":           map[string]any{"type": "string", "description": "Short human label echoed in the follow-up (e.g. 'nightly-cognition rerun')."},
			"note":            map[string]any{"type": "string", "description": "Optional extra context to echo back when it settles."},
			"timeout_minutes": map[string]any{"type": "integer", "description": "Stop watching after this many minutes if it never settles (default 30)."},
		},
	}
}

func (t *watchUntil) Execute(ctx context.Context, in map[string]any) (string, error) {
	sessionID := SessionIDFromContext(ctx)
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("watch_until needs a chat session to report back into")
	}

	runID := strings.TrimSpace(strString(in, "run_id"))
	cronRef := strings.TrimSpace(strString(in, "cron"))
	label := strings.TrimSpace(strString(in, "label"))
	note := strings.TrimSpace(strString(in, "note"))

	timeout := 30 * time.Minute
	if m := intFrom(in, "timeout_minutes"); m > 0 {
		timeout = time.Duration(m) * time.Minute
	}

	w := watch.Watch{
		SessionID:   sessionID,
		TargetLabel: label,
		Note:        note,
		Timeout:     timeout,
	}

	switch {
	case runID != "":
		w.Kind = "run"
		w.TargetID = runID
		if w.TargetLabel == "" {
			w.TargetLabel = "the run"
		}
	case cronRef != "":
		cronID, name, err := t.resolveCron(ctx, cronRef)
		if err != nil {
			return "", err
		}
		w.Kind = "cron"
		w.TargetID = cronID
		if w.TargetLabel == "" {
			w.TargetLabel = name
		}
	default:
		return "", errors.New("watch_until needs either run_id or cron")
	}

	id, err := t.store.Create(ctx, w)
	if err != nil {
		return "", err
	}
	until := fmt.Sprintf("until %s settles (or %dm timeout)", w.TargetLabel, int(timeout.Minutes()))
	return fmt.Sprintf(`{"ok":true,"watch_id":%q,"watching":%q,"detail":%q,"delivers_to":"this chat"}`,
		id, w.TargetLabel, until), nil
}

// resolveCron accepts a cron id or name and returns (id, name). A bare uuid is
// taken as an id; anything else is looked up by name in mem_crons.
func (t *watchUntil) resolveCron(ctx context.Context, ref string) (id, name string, err error) {
	if t.pool == nil {
		return "", "", errors.New("watch_until: no pool to resolve cron")
	}
	if _, perr := uuid.Parse(ref); perr == nil {
		// It's an id — fetch the name for a nice label.
		var nm string
		if qerr := t.pool.QueryRow(ctx, `SELECT name FROM mem_crons WHERE id = $1::uuid`, ref).Scan(&nm); qerr != nil {
			return "", "", fmt.Errorf("cron id %q not found: %w", ref, qerr)
		}
		return ref, nm, nil
	}
	resolved, rerr := resolveCronIDByName(ctx, t.pool, ref)
	if rerr != nil {
		return "", "", rerr
	}
	return resolved, ref, nil
}
