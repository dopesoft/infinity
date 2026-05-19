// store.go - mem_calendar_events + mem_calendar_sync_state read/write.
//
// The native sync owns these writes. The agent never UPDATEs these
// tables directly; calendar_* tools call into Sync / Provider methods
// which use this store. One shape for everything.

package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncState is one mem_calendar_sync_state row.
type SyncState struct {
	AccountID    string
	CalendarID   string
	SyncToken    string
	LastFullSync time.Time
	LastTickAt   time.Time
	LastError    string
	EventsSeen   int
}

// Store wraps the small SQL surface the calendar package needs. Cheap
// to construct; one instance per pool is fine.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store bound to pool. nil-safe construction; every
// method short-circuits when pool is nil so a no-DB test or partial
// boot doesn't panic.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// LoadSyncState reads (or creates a fresh zero-value row for) the cursor
// for one (account, calendar) pair.
func (s *Store) LoadSyncState(ctx context.Context, accountID, calendarID string) (*SyncState, error) {
	if s == nil || s.pool == nil {
		return &SyncState{AccountID: accountID, CalendarID: calendarID}, nil
	}
	if calendarID == "" {
		calendarID = defaultCalendarID
	}
	var st SyncState
	st.AccountID = accountID
	st.CalendarID = calendarID
	var (
		token        *string
		lastFullSync *time.Time
		lastTickAt   *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT sync_token, last_full_sync, last_tick_at, last_error, events_seen
		  FROM mem_calendar_sync_state
		 WHERE account_id = $1 AND calendar_id = $2
	`, accountID, calendarID).Scan(&token, &lastFullSync, &lastTickAt, &st.LastError, &st.EventsSeen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &st, nil
		}
		return nil, err
	}
	if token != nil {
		st.SyncToken = *token
	}
	if lastFullSync != nil {
		st.LastFullSync = *lastFullSync
	}
	if lastTickAt != nil {
		st.LastTickAt = *lastTickAt
	}
	return &st, nil
}

// SaveSyncState upserts the cursor row. Always bumps updated_at + last_tick_at.
func (s *Store) SaveSyncState(ctx context.Context, st *SyncState) error {
	if s == nil || s.pool == nil || st == nil {
		return nil
	}
	calID := st.CalendarID
	if calID == "" {
		calID = defaultCalendarID
	}
	var tokenArg any
	if st.SyncToken != "" {
		tokenArg = st.SyncToken
	} else {
		tokenArg = nil
	}
	var fullSyncArg any
	if !st.LastFullSync.IsZero() {
		fullSyncArg = st.LastFullSync
	} else {
		fullSyncArg = nil
	}
	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_calendar_sync_state
		    (account_id, calendar_id, sync_token, last_full_sync, last_tick_at, last_error, events_seen, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (account_id, calendar_id) DO UPDATE SET
		    sync_token     = EXCLUDED.sync_token,
		    last_full_sync = COALESCE(EXCLUDED.last_full_sync, mem_calendar_sync_state.last_full_sync),
		    last_tick_at   = EXCLUDED.last_tick_at,
		    last_error     = EXCLUDED.last_error,
		    events_seen    = mem_calendar_sync_state.events_seen + EXCLUDED.events_seen,
		    updated_at     = EXCLUDED.updated_at
	`, st.AccountID, calID, tokenArg, fullSyncArg, now, st.LastError, st.EventsSeen, now)
	return err
}

// UpsertEvent writes one canonical event to mem_calendar_events. Dedupe
// keys: (source='google_calendar', source_ref->>'remote_id'). On a
// cancelled event we hard-delete instead so the dashboard never shows a
// "ghost" row that the calendar no longer believes in.
func (s *Store) UpsertEvent(ctx context.Context, ev *Event) error {
	if s == nil || s.pool == nil || ev == nil {
		return nil
	}
	if ev.Status == "cancelled" {
		_, err := s.pool.Exec(ctx, `
			DELETE FROM mem_calendar_events
			 WHERE source = $1
			   AND source_ref->>'remote_id' = $2
		`, providerSlug, ev.RemoteID)
		return err
	}
	attendeesJSON, err := json.Marshal(ev.Attendees)
	if err != nil {
		return fmt.Errorf("marshal attendees: %w", err)
	}
	organizerJSON, err := json.Marshal(ev.Organizer)
	if err != nil {
		return fmt.Errorf("marshal organizer: %w", err)
	}
	conferenceJSON, err := json.Marshal(ev.Conference)
	if err != nil {
		return fmt.Errorf("marshal conference: %w", err)
	}
	recurrenceJSON, err := json.Marshal(ev.Recurrence)
	if err != nil {
		return fmt.Errorf("marshal recurrence: %w", err)
	}
	sourceRef := map[string]any{
		"remote_id":   ev.RemoteID,
		"calendar_id": ev.CalendarID,
		"account_id":  ev.AccountID,
		"html_link":   ev.HTMLLink,
	}
	sourceRefJSON, err := json.Marshal(sourceRef)
	if err != nil {
		return fmt.Errorf("marshal source_ref: %w", err)
	}

	var endsAtArg any
	if !ev.EndsAt.IsZero() {
		endsAtArg = ev.EndsAt
	} else {
		endsAtArg = nil
	}

	classification := ev.Classification
	if classification == "" {
		classification = inferClassification(ev)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_calendar_events (
		    title, location, attendees, starts_at, ends_at, all_day,
		    classification, prep, source, source_ref,
		    account_id, calendar_id, status, description,
		    html_link, hangout_link, conference, organizer,
		    response_status, recurrence, remote_etag, updated_at
		) VALUES (
		    $1, $2, $3::jsonb, $4, $5, $6,
		    $7, '[]'::jsonb, $8, $9::jsonb,
		    $10, $11, $12, $13,
		    $14, $15, $16::jsonb, $17::jsonb,
		    $18, $19::jsonb, $20, NOW()
		)
		ON CONFLICT (source, (source_ref->>'remote_id')) DO UPDATE SET
		    title           = EXCLUDED.title,
		    location        = EXCLUDED.location,
		    attendees       = EXCLUDED.attendees,
		    starts_at       = EXCLUDED.starts_at,
		    ends_at         = EXCLUDED.ends_at,
		    all_day         = EXCLUDED.all_day,
		    classification  = EXCLUDED.classification,
		    source_ref      = EXCLUDED.source_ref,
		    account_id      = EXCLUDED.account_id,
		    calendar_id     = EXCLUDED.calendar_id,
		    status          = EXCLUDED.status,
		    description     = EXCLUDED.description,
		    html_link       = EXCLUDED.html_link,
		    hangout_link    = EXCLUDED.hangout_link,
		    conference      = EXCLUDED.conference,
		    organizer       = EXCLUDED.organizer,
		    response_status = EXCLUDED.response_status,
		    recurrence      = EXCLUDED.recurrence,
		    remote_etag     = EXCLUDED.remote_etag,
		    updated_at      = NOW()
	`,
		ev.Title, ev.Location, attendeesJSON, ev.StartsAt, endsAtArg, ev.AllDay,
		classification, providerSlug, sourceRefJSON,
		ev.AccountID, ev.CalendarID, ev.Status, ev.Description,
		ev.HTMLLink, ev.HangoutLink, conferenceJSON, organizerJSON,
		nullableString(ev.ResponseStatus), recurrenceJSON, ev.RemoteEtag,
	)
	return err
}

// FindEvent locates the (account_id, calendar_id, remote_id) for one
// row by its mem_calendar_events.id - the Studio uses the local UUID
// when firing /respond, so the API has to look up the remote pointer.
func (s *Store) FindEvent(ctx context.Context, localID string) (accountID, calendarID, remoteID string, err error) {
	if s == nil || s.pool == nil {
		return "", "", "", fmt.Errorf("no pool")
	}
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(account_id, ''),
		       COALESCE(calendar_id, 'primary'),
		       COALESCE(source_ref->>'remote_id', '')
		  FROM mem_calendar_events
		 WHERE id = $1
		   AND source = $2
	`, localID, providerSlug).Scan(&accountID, &calendarID, &remoteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("event %s not found", localID)
	}
	return
}

// PatchResponseStatus is a tiny optimistic write applied before the next
// sync tick confirms the upstream change. Lets the modal flip to the new
// state instantly without waiting 0-2 min for sync.
func (s *Store) PatchResponseStatus(ctx context.Context, localID, status string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_calendar_events
		   SET response_status = $1, updated_at = NOW()
		 WHERE id = $2
	`, nullableString(status), localID)
	return err
}

// nullableString returns nil for empty strings so JSON null lands in the
// column instead of "" - matters for response_status which the dashboard
// renderer treats as "is RSVP needed" vs "boss already responded".
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// inferClassification is a tiny deterministic label so the agent's
// generic prep heuristics still work even before any LLM has touched
// the event. Cheap pattern match; falls back to "meeting" because
// most calendar items are meetings.
func inferClassification(ev *Event) string {
	t := ev.Title
	for _, kw := range []string{"flight", "dinner", "lunch", "interview", "appointment", "concert", "wedding"} {
		if containsFold(t, kw) {
			return kw
		}
	}
	if len(ev.Attendees) > 0 || ev.HangoutLink != "" {
		return "meeting"
	}
	return "other"
}

// containsFold: case-insensitive substring match. Hot path so we don't
// call strings.ToLower on every event in the page.
func containsFold(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	S, T := []byte(s), []byte(substr)
	for i := 0; i+len(T) <= len(S); i++ {
		match := true
		for j := 0; j < len(T); j++ {
			a, b := S[i+j], T[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
