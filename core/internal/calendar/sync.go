// sync.go - one-tick incremental sync for a single account.
//
// Driven by the ticker (ticker.go) or by an on-demand API call. Each
// RunOnce walks one (account, calendar) cursor: load syncToken from the
// store, fetch a page (or full window if no token), upsert each event,
// loop until Google's response carries nextSyncToken. Then write the
// new token back. 410 GONE triggers a wipe + full resync next tick.
//
// Hooks fire on each successful tick so the agent memory pipeline
// records what happened (sync ran, N events written) - same pattern
// as the connector poller.

package calendar

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
)

// infoLog routes calendar success lines to stdout so Railway tags them
// as info, not error (see CLAUDE.md "Logging - severity must match
// reality"). log.Printf would default to stderr.
var infoLog = log.New(os.Stdout, "calendar: ", log.LstdFlags)

// SyncResult summarises one RunOnce. Returned to callers (API + ticker)
// so they can render "synced X events in Yms" feedback.
type SyncResult struct {
	AccountID   string `json:"account_id"`
	CalendarID  string `json:"calendar_id"`
	Pages       int    `json:"pages"`
	Events      int    `json:"events"`
	DurationMS  int64  `json:"duration_ms"`
	FullResync  bool   `json:"full_resync"`
	Error       string `json:"error,omitempty"`
}

// Syncer orchestrates Provider + Store + hook pipeline. Long-lived; one
// per process.
type Syncer struct {
	provider Provider
	store    *Store
	pipeline *hooks.Pipeline
}

// NewSyncer constructs the orchestrator. provider + store required;
// pipeline may be nil (capture skipped silently).
func NewSyncer(provider Provider, store *Store, pipeline *hooks.Pipeline) *Syncer {
	return &Syncer{provider: provider, store: store, pipeline: pipeline}
}

// RunOnce executes one incremental sync. Safe to call repeatedly; the
// per-account row in mem_calendar_sync_state is the cursor.
func (s *Syncer) RunOnce(ctx context.Context, accountID, calendarID string) SyncResult {
	if calendarID == "" {
		calendarID = defaultCalendarID
	}
	res := SyncResult{AccountID: accountID, CalendarID: calendarID}
	start := time.Now()

	state, err := s.store.LoadSyncState(ctx, accountID, calendarID)
	if err != nil {
		res.Error = err.Error()
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}

	syncToken := state.SyncToken
	pageToken := ""
	var nextSync string

	for {
		page, err := s.provider.List(ctx, accountID, ListOptions{
			CalendarID: calendarID,
			SyncToken:  syncToken,
			PageToken:  pageToken,
		})
		if err != nil {
			res.Error = err.Error()
			break
		}
		// 410 recovery: wipe the token in-memory and let the next loop
		// iteration kick off a full sync immediately.
		if page.FullResync {
			res.FullResync = true
			syncToken = ""
			pageToken = ""
			state.SyncToken = ""
			state.LastError = "sync token expired - full resync"
			// fall through to next iteration: we just consumed nothing
			// useful, but the loop will now re-call List with no token.
			continue
		}

		res.Pages++
		for i := range page.Events {
			ev := page.Events[i]
			if err := s.store.UpsertEvent(ctx, &ev); err != nil {
				// Log but don't abort the page - one bad row shouldn't
				// drop the rest of the delta.
				slog.Warn("calendar upsert", "account", accountID, "remote_id", ev.RemoteID, "err", err.Error())
				continue
			}
			res.Events++
		}

		if page.NextPage != "" {
			pageToken = page.NextPage
			continue
		}
		// End of delta - record the new syncToken (may still be empty
		// when Google returned no nextSyncToken yet, in which case the
		// next tick is a full sync).
		nextSync = page.SyncToken
		break
	}

	// Persist new cursor + bookkeeping. We bump events_seen by THIS run's
	// count and let the store add to the existing total.
	state.SyncToken = nextSync
	if syncToken == "" {
		state.LastFullSync = time.Now()
	}
	state.LastTickAt = time.Now()
	state.EventsSeen = res.Events
	if res.Error != "" {
		state.LastError = res.Error
	} else if state.LastError != "" {
		state.LastError = ""
	}
	if err := s.store.SaveSyncState(ctx, state); err != nil {
		slog.Warn("calendar save sync state", "account", accountID, "err", err.Error())
	}

	res.DurationMS = time.Since(start).Milliseconds()

	// Emit hook so the capture pipeline records the sync. Even an
	// empty tick is observation-worthy.
	if s.pipeline != nil {
		s.pipeline.Emit(hooks.Event{
			Name: hooks.PostToolUse,
			Text: fmt.Sprintf("calendar.sync %s/%s: pages=%d events=%d full=%v",
				accountID, calendarID, res.Pages, res.Events, res.FullResync),
			Payload: map[string]any{
				"tool":   "calendar.sync",
				"result": res,
			},
		})
	}

	infoLog.Printf("sync %s/%s: pages=%d events=%d duration=%dms full=%v",
		accountID, calendarID, res.Pages, res.Events, res.DurationMS, res.FullResync)
	return res
}

// Respond writes the boss's RSVP and immediately patches the local row
// for an optimistic UI update. The next tick reconciles.
func (s *Syncer) Respond(ctx context.Context, localID string, response Response) (*Event, error) {
	accountID, calendarID, remoteID, err := s.store.FindEvent(ctx, localID)
	if err != nil {
		return nil, err
	}
	ev, err := s.provider.Respond(ctx, accountID, calendarID, remoteID, response)
	if err != nil {
		return nil, err
	}
	// Persist the canonical updated row (writes the new attendees state
	// + response_status). The optimistic local PatchResponseStatus would
	// be a no-op now since we already have the authoritative version.
	if err := s.store.UpsertEvent(ctx, ev); err != nil {
		// Surface but still return ev so the UI updates from the
		// provider response.
		slog.Warn("calendar upsert after rsvp", "local", localID, "err", err.Error())
	}
	return ev, nil
}

// SyncNow runs one tick for an account on demand (Studio button, agent
// tool). Returns the same SyncResult as the ticker.
func (s *Syncer) SyncNow(ctx context.Context, accountID, calendarID string) SyncResult {
	return s.RunOnce(ctx, accountID, calendarID)
}
