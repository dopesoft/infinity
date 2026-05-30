package push

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CalendarTicker scans mem_calendar_events on a slow cadence and pushes a
// banner when an event is starting soon. It's the absent third leg of the
// "boss wanted notifications" trio (followups + runs already fire from
// their own write sites; calendar has no synchronous write site we can
// hook because Google sync writes a batch, not a "this is starting now"
// signal — so a poller is the right shape here, not a hook).
//
// Cadence: tick once a minute. Window: events whose starts_at is within
// [now, now+15min). Dedup: an in-memory set of event ids we've already
// notified in this process. A restart re-notifies — that's fine; better
// to re-buzz once than miss the event. The set is bounded by sweeping
// past-window ids on every tick so it can't grow without bound.
type CalendarTicker struct {
	pool     *pgxpool.Pool
	notifier *CalendarUpcomingNotifier
	logger   *slog.Logger
	window   time.Duration

	mu       sync.Mutex
	notified map[string]time.Time
}

// NewCalendarTicker wires the ticker. nil pool / nil notifier produces a
// nil-safe no-op (Start returns immediately).
func NewCalendarTicker(pool *pgxpool.Pool, notifier *CalendarUpcomingNotifier, logger *slog.Logger) *CalendarTicker {
	if logger == nil {
		logger = slog.Default()
	}
	return &CalendarTicker{
		pool:     pool,
		notifier: notifier,
		logger:   logger,
		window:   15 * time.Minute,
		notified: map[string]time.Time{},
	}
}

// Start runs the ticker until ctx is cancelled. Safe to call once at boot
// (`go ticker.Start(ctx)`); idempotent in the sense that a second Start
// just spawns a duplicate loop, so don't do that.
func (t *CalendarTicker) Start(ctx context.Context) {
	if t == nil || t.pool == nil || t.notifier == nil || t.notifier.Sender == nil {
		return
	}
	// Initial scan so a freshly-started container catches imminent events
	// without waiting a full tick.
	t.scanOnce(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.scanOnce(ctx)
		}
	}
}

func (t *CalendarTicker) scanOnce(ctx context.Context) {
	now := time.Now()
	cutoff := now.Add(t.window)
	rows, err := t.pool.Query(ctx, `
		SELECT id, title, starts_at,
		       COALESCE(location, ''),
		       COALESCE(hangout_link, '')
		FROM mem_calendar_events
		WHERE starts_at >= $1
		  AND starts_at <= $2
		  AND COALESCE(all_day, FALSE) = FALSE
		ORDER BY starts_at ASC
		LIMIT 50
	`, now, cutoff)
	if err != nil {
		t.logger.Warn("push: calendar ticker query", "err", err)
		return
	}
	defer rows.Close()

	type row struct {
		ID       string
		Title    string
		Starts   time.Time
		Location string
		Hangout  string
	}
	var queue []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Title, &r.Starts, &r.Location, &r.Hangout); err != nil {
			continue
		}
		queue = append(queue, r)
	}

	t.mu.Lock()
	// Sweep notified ids whose events are now in the past — bounded set.
	for id, when := range t.notified {
		if when.Before(now.Add(-1 * time.Hour)) {
			delete(t.notified, id)
		}
	}
	pending := make([]row, 0, len(queue))
	for _, r := range queue {
		if _, seen := t.notified[r.ID]; seen {
			continue
		}
		t.notified[r.ID] = r.Starts
		pending = append(pending, r)
	}
	t.mu.Unlock()

	for _, r := range pending {
		t.notifier.NotifyEventUpcoming(ctx, r.ID, r.Title, r.Starts, r.Location, r.Hangout)
	}
}
