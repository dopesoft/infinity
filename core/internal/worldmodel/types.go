// Package worldmodel implements the world model + agent-owned goals -
// Phase 5 of the assembly substrate.
//
// Honcho models the boss; mem_memories is episodic recall. The world model
// is the third thing: a structured, queryable model of the boss's WORLD -
// the people, projects, accounts, and threads the agent acts on, and their
// current state - plus the agent's OWN durable goals, each carrying a
// living plan the agent re-writes as it makes (or fails to make) progress.
//
// The agent reads/writes via the entity_* and goal_* tools, never raw SQL.
package worldmodel

import "time"

// Entity is one node of the world model.
type Entity struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"` // person | project | account | org | thread | …
	Name       string         `json:"name"`
	Aliases    []string       `json:"aliases"`
	Attributes map[string]any `json:"attributes"`
	Summary    string         `json:"summary,omitempty"`
	Status     string         `json:"status"` // active | dormant | archived
	Salience   int            `json:"salience"`
	LastSeenAt time.Time      `json:"lastSeenAt"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	Links      []LinkView     `json:"links,omitempty"`
}

// LinkView is a resolved edge - the relation plus the entity on the other
// end, with the direction relative to the entity being viewed.
type LinkView struct {
	Direction  string `json:"direction"` // "out" (this → other) | "in" (other → this)
	Relation   string `json:"relation"`
	OtherID    string `json:"otherId"`
	OtherName  string `json:"otherName"`
	OtherKind  string `json:"otherKind"`
	Note       string `json:"note,omitempty"`
}

// PlanItem is one step of an agent goal's living plan.
type PlanItem struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

// Goal is one of the agent's own objectives.
type Goal struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Status         string     `json:"status"`   // active | blocked | done | abandoned
	Priority       string     `json:"priority"` // low | med | high
	Plan           []PlanItem `json:"plan"`
	Progress       string     `json:"progress,omitempty"`
	Blocker        string     `json:"blocker,omitempty"`
	EntityID       string     `json:"entityId,omitempty"`
	DueAt          *time.Time `json:"dueAt,omitempty"`
	LastProgressAt time.Time  `json:"lastProgressAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// GoalPatch is a partial update to a goal. Nil fields are left untouched;
// a non-nil ProgressAppend is appended to the running progress narrative.
type GoalPatch struct {
	Status         *string
	Priority       *string
	Plan           *[]PlanItem
	ProgressAppend *string
	Blocker        *string
	DueAt          *time.Time
}
