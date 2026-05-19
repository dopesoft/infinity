// Package calendar - native Google Calendar sync substrate.
//
// Rule #1a building block. Composio holds the OAuth grant; we call
// Google's API directly through Composio's tools.proxy passthrough so
// the access token never leaves Composio and the sync logic stays
// Google-shaped (incremental syncToken, etag-aware patches, full
// attendee shapes, conferenceData entry points). Zero LLM calls in
// the sync path - this is pure infrastructure.
//
// The agent still acts on the calendar through Provider tools
// (calendar_list / calendar_respond / calendar_create) which delegate
// to the same package, so there is one shape for "calendar action"
// across deterministic sync, agent intent, and Studio UI clicks.
//
// Layering:
//
//   provider.go   - Provider interface (List / Respond / Patch / Create / Delete)
//   google.go     - Google Calendar v3 implementation via Composio Proxy
//   sync.go       - per-account incremental sync, upsert into mem_calendar_events
//   ticker.go     - background loop, 2-min cadence per googlecalendar account
//   store.go      - sync-state read/write (mem_calendar_sync_state)
//   tools.go      - agent tools (calendar_list / calendar_respond / calendar_create)
//   api.go        - HTTP endpoints for Studio (Accept / Decline / Sync now)
package calendar

import (
	"context"
	"time"
)

// Attendee mirrors Google Calendar's attendee shape. We carry the full
// object (not just the email string) so the Studio modal can render
// avatar chips + response-status icons matching the attached design.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"display_name,omitempty"`
	ResponseStatus string `json:"response_status,omitempty"` // accepted | declined | tentative | needsAction
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
}

// ConferenceEntryPoint is one row of Google's conferenceData.entryPoints.
// We surface video entry points (Meet / Zoom / Teams) as a clickable
// "Join …" row in the modal.
type ConferenceEntryPoint struct {
	Type  string `json:"type"`            // "video" | "phone" | "sip" | "more"
	URI   string `json:"uri"`             // tel:+1… or https://meet.google.com/…
	Label string `json:"label,omitempty"` // user-friendly label, optional
}

// ConferenceData is our compressed projection of Google's conferenceData.
// We only persist what the UI renders; the raw blob stays at the API edge.
type ConferenceData struct {
	SolutionName string                 `json:"solution_name,omitempty"` // "Google Meet" | "Zoom" | …
	IconURI      string                 `json:"icon_uri,omitempty"`
	EntryPoints  []ConferenceEntryPoint `json:"entry_points,omitempty"`
}

// Organizer is the event organizer (email + display name). May overlap
// with one of the Attendees rows; the Studio renderer treats organizer
// as a separate header line above the attendee list.
type Organizer struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// Event is the canonical shape we use in-process. Maps roughly 1:1 to
// Google's Event resource but with stable snake_case fields and an
// account_id back-reference so multi-account boss-state never collides.
type Event struct {
	// Composio connected_account ("ca_xxx") that owns this event. The
	// (AccountID, CalendarID, RemoteID) triple uniquely identifies an
	// event across the system.
	AccountID  string `json:"account_id"`
	CalendarID string `json:"calendar_id"`
	RemoteID   string `json:"remote_id"`
	RemoteEtag string `json:"remote_etag,omitempty"`

	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	HTMLLink    string    `json:"html_link,omitempty"`
	HangoutLink string    `json:"hangout_link,omitempty"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at,omitempty"`
	AllDay      bool      `json:"all_day,omitempty"`

	Organizer      Organizer       `json:"organizer,omitempty"`
	Attendees      []Attendee      `json:"attendees,omitempty"`
	Conference     ConferenceData  `json:"conference,omitempty"`
	Recurrence     []string        `json:"recurrence,omitempty"`
	Status         string          `json:"status,omitempty"`          // confirmed | tentative | cancelled
	ResponseStatus string          `json:"response_status,omitempty"` // boss's own status (self attendee)
	Classification string          `json:"classification,omitempty"`  // optional agent label
}

// ListPage is the result of one Provider.List call. SyncToken is
// Google's nextSyncToken when fully exhausted; PageToken non-empty
// when more pages remain on this delta. Cancellations come back as
// Event entries with Status="cancelled" - the sync writer hard-deletes
// the matching row.
type ListPage struct {
	Events     []Event
	NextPage   string // pageToken for the next page (paginating within one sync)
	SyncToken  string // nextSyncToken (only set on the final page)
	FullResync bool   // true when Google returned 410 GONE; caller wipes token + reloads
}

// ListOptions controls one events.list call.
type ListOptions struct {
	CalendarID string    // "primary" by default
	SyncToken  string    // incremental cursor; empty = full sync
	PageToken  string    // pagination within a delta
	TimeMin    time.Time // for full sync only (skip past events older than this)
	TimeMax    time.Time // for full sync only (cap the window e.g. +6 months)
	MaxResults int       // page size; default 250 (Google max)
}

// Response is the boss's RSVP on an event.
type Response string

const (
	Accepted    Response = "accepted"
	Declined    Response = "declined"
	Tentative   Response = "tentative"
	NeedsAction Response = "needsAction"
)

// Valid returns true when r is a writeable response value (NeedsAction
// is read-only; we only ever write accepted/declined/tentative).
func (r Response) Valid() bool {
	switch r {
	case Accepted, Declined, Tentative:
		return true
	}
	return false
}

// Provider is the back-end-agnostic surface every calendar implementation
// satisfies. Google is the only impl in v1; Microsoft / CalDAV slot in
// behind the same interface later.
type Provider interface {
	// Slug returns a short identifier for this provider ("google_calendar").
	// Used as the value written into mem_calendar_events.source.
	Slug() string

	// List fetches a page of events (incremental when SyncToken set,
	// full when not). Returns ListPage.FullResync=true on a 410 GONE so
	// the caller can wipe state and retry.
	List(ctx context.Context, accountID string, opts ListOptions) (*ListPage, error)

	// Respond sets the boss's RSVP on an event. response must be Valid().
	// CalendarID defaults to "primary" when empty.
	Respond(ctx context.Context, accountID, calendarID, remoteID string, response Response) (*Event, error)

	// Patch applies a partial update (title/start/end/description/location).
	// CalendarID defaults to "primary".
	Patch(ctx context.Context, accountID, calendarID, remoteID string, patch map[string]any) (*Event, error)

	// Create writes a new event. CalendarID defaults to "primary".
	// Returns the canonical event from Google after creation.
	Create(ctx context.Context, accountID, calendarID string, draft map[string]any) (*Event, error)

	// Delete removes an event. CalendarID defaults to "primary".
	Delete(ctx context.Context, accountID, calendarID, remoteID string) error
}
