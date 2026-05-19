// ticker.go - background loop that runs Syncer.RunOnce per account on a
// fixed cadence. Discovers accounts via the connectors.Cache snapshot
// (toolkit_slug = "googlecalendar"); auto-picks up new accounts on the
// next tick without a restart.
//
// Cadence: every TickInterval (default 2 min). One tick walks every
// connected Google Calendar account in series. Google's syncToken keeps
// each call small (only deltas), so even half a dozen accounts is well
// under the API quota.

package calendar

import (
	"context"
	"sync"
	"time"
)

// Ticker drives Syncer on a schedule.
type Ticker struct {
	sync     *Syncer
	listFn   func() []string // returns connected googlecalendar account ids
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewTicker constructs the ticker. listFn must return the current list
// of connected Google Calendar account ids. interval defaults to 2m.
func NewTicker(syncer *Syncer, listFn func() []string, interval time.Duration) *Ticker {
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	return &Ticker{sync: syncer, listFn: listFn, interval: interval}
}

// Start launches the loop. Returns immediately. Safe to call multiple
// times; subsequent calls stop the previous loop first.
func (t *Ticker) Start(parent context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	go t.loop(ctx)
}

// Stop halts the loop.
func (t *Ticker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

func (t *Ticker) loop(ctx context.Context) {
	// Stagger the first tick by 15s so we don't pile sync work onto the
	// boot-time burst (catalog sync, identity discovery, etc).
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		t.runOnceAll(ctx)
		timer.Reset(t.interval)
	}
}

// runOnceAll syncs every connected account exactly once. Errors per
// account are logged inside Syncer.RunOnce; we never abort the loop.
func (t *Ticker) runOnceAll(ctx context.Context) {
	accounts := t.listFn()
	for _, id := range accounts {
		// Each tick gets its own short-lived context so a hung HTTP
		// call to Composio can't stall the whole ticker.
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_ = t.sync.RunOnce(callCtx, id, defaultCalendarID)
		cancel()
	}
}
