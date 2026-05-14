// Package surface implements the generic dashboard SURFACE CONTRACT.
//
// Rule #1 substrate. Rather than a bespoke table + bespoke Go scorer +
// bespoke widget per source, ANY producer — a skill recipe, a connector
// poll, a cron, the agent mid-conversation — writes ranked, structured
// items through one contract (mem_surface_items). Studio renders them
// generically by Surface + Kind. A new capability lands on the dashboard
// with zero new table, zero new loader, zero new widget.
//
// The agent never writes this table with raw SQL. The surface_item /
// surface_update native tools (core/internal/tools/surface_tools.go) ARE
// the contract — the boundary the LLM assembles against.
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
	ID               string         `json:"id"`
	Surface          string         `json:"surface"`
	Kind             string         `json:"kind"`
	Source           string         `json:"source"`
	ExternalID       string         `json:"externalId,omitempty"`
	Title            string         `json:"title"`
	Subtitle         string         `json:"subtitle,omitempty"`
	Body             string         `json:"body,omitempty"`
	URL              string         `json:"url,omitempty"`
	Importance       *int           `json:"importance,omitempty"`
	ImportanceReason string         `json:"importanceReason,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	Status           Status         `json:"status"`
	SnoozedUntil     *time.Time     `json:"snoozedUntil,omitempty"`
	ExpiresAt        *time.Time     `json:"expiresAt,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
	ScoredAt         *time.Time     `json:"scoredAt,omitempty"`
}

// Patch is the set of fields a surface_update call may change. Nil fields
// are left untouched (COALESCE-style partial update).
type Patch struct {
	Status           *Status
	Importance       *int
	ImportanceReason *string
	SnoozedUntil     *time.Time
}
