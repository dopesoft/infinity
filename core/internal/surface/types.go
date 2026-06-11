// Package surface implements the generic dashboard SURFACE CONTRACT.
//
// Rule #1 substrate. Rather than a bespoke table + bespoke Go scorer +
// bespoke widget per source, ANY producer - a skill recipe, a connector
// poll, a cron, the agent mid-conversation - writes ranked, structured
// items through one contract (mem_surface_items). Studio renders them
// generically by Surface + Kind. A new capability lands on the dashboard
// with zero new table, zero new loader, zero new widget.
//
// The agent never writes this table with raw SQL. The surface_item /
// surface_update native tools (core/internal/tools/surface_tools.go) ARE
// the contract - the boundary the LLM assembles against.
package surface

import "time"

// Status is the lifecycle of a surfaced item.
type Status string

const (
	StatusOpen      Status = "open"
	StatusSnoozed   Status = "snoozed"
	StatusDone      Status = "done"
	StatusDismissed Status = "dismissed"
)

func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusSnoozed, StatusDone, StatusDismissed:
		return true
	}
	return false
}

// Item is one row of the generic dashboard surface contract.
type Item struct {
	ID         string `json:"id"`
	Surface    string `json:"surface"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	ExternalID string `json:"externalId,omitempty"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	Body       string `json:"body,omitempty"`
	// CachedHTML / CachedText carry a pre-rendered message body (e.g. the full
	// HTML email a triage recipe already fetched) so the dashboard renders it
	// WITHOUT a live connector call - durable even if the connector is later
	// revoked. Write-mostly: deliberately NOT selected by the list queries (an
	// HTML email is 50-200KB and would bloat every list load); the
	// single-message endpoint reads these columns directly.
	CachedHTML       string         `json:"cachedHtml,omitempty"`
	CachedText       string         `json:"cachedText,omitempty"`
	URL              string         `json:"url,omitempty"`
	Importance       *int           `json:"importance,omitempty"`
	ImportanceReason string         `json:"importanceReason,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	// Actions are boss-tappable controls rendered on the dashboard card.
	// Tapping one fires POST /api/surface/action which seeds an autonomous
	// agent turn prompted with the action's Intent + this item's context -
	// the "surface return-path". Empty for a render-only item.
	Actions      []Action   `json:"actions,omitempty"`
	Status       Status     `json:"status"`
	SnoozedUntil *time.Time `json:"snoozedUntil,omitempty"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	ScoredAt     *time.Time `json:"scoredAt,omitempty"`
	// Reopen marks a ROLLING item (one card per producer, refreshed every run
	// — e.g. a cron's nightly outcome) whose upsert carries genuinely NEW
	// information: if the existing row was dismissed, it flips back to open so
	// the fresh outcome reaches the boss's inbox. Without this, dismissing a
	// cron's card once silently swallowed every later night's outcome
	// (observed 2026-06-11: triage/self-improve/verify outcomes invisible
	// behind a yesterday's dismissal). One-shot items (emails, alerts) leave
	// this false — a dismissed email stays dismissed forever ("Follow-ups
	// never auto-resolved" cuts both ways). Done/snoozed rows are never
	// touched either way.
	Reopen bool `json:"-"`
}

// Action is a boss-tappable control on a surfaced item - the return half of
// the surface contract. The agent attaches actions when it surfaces something
// actionable ("Draft reply", "Snooze 1d", "Open thread"); the dashboard
// renders them; tapping one POSTs {id, action_id} to /api/surface/action,
// which runs Intent as an autonomous agent turn against this item. Generic:
// any producer invents actions, no per-action Go anywhere.
type Action struct {
	ID     string `json:"id"`              // stable id, unique within the item
	Label  string `json:"label"`           // button text shown to the boss
	Intent string `json:"intent"`          // natural-language instruction the agent runs
	Style  string `json:"style,omitempty"` // UI hint: "primary" | "default" | "danger"
}

// Patch is the set of fields a surface_update call may change. Nil fields
// are left untouched (COALESCE-style partial update).
type Patch struct {
	Status           *Status
	Importance       *int
	ImportanceReason *string
	SnoozedUntil     *time.Time
	MetadataMerge    map[string]any
}
