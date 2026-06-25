package hooks

import "time"

// EventName enumerates the 12 hook events listed in the spec (PDF p.19).
type EventName string

const (
	SessionStart       EventName = "SessionStart"
	UserPromptSubmit   EventName = "UserPromptSubmit"
	PreToolUse         EventName = "PreToolUse"
	PostToolUse        EventName = "PostToolUse"
	PostToolUseFailure EventName = "PostToolUseFailure"
	PreCompact         EventName = "PreCompact"
	SubagentStart      EventName = "SubagentStart"
	SubagentStop       EventName = "SubagentStop"
	Notification       EventName = "Notification"
	TaskCompleted      EventName = "TaskCompleted"
	Stop               EventName = "Stop"
	SessionEnd         EventName = "SessionEnd"
	// SelfHealResolved fires when a reactive self-heal pass converted an
	// unresolved failure into a fixed+verified outcome within the same turn.
	// SelfHealExhausted fires when the heal passes ran out and the turn still
	// ended on the failure. Both are consumed by the self-heal encoder, which
	// writes the receipt (run/surface narrative) and the durable guard
	// (procedural memory + structural code/skill proposal). NOT in AllEvents:
	// they drive a targeted listener, not the default capture chain.
	SelfHealResolved  EventName = "SelfHealResolved"
	SelfHealExhausted EventName = "SelfHealExhausted"
)

// AllEvents is the canonical list, useful for default hook registration.
var AllEvents = []EventName{
	SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure,
	PreCompact, SubagentStart, SubagentStop, Notification, TaskCompleted, Stop, SessionEnd,
}

// Event is the payload that fires through the pipeline.
type Event struct {
	Name      EventName
	SessionID string
	Project   string
	Payload   map[string]any
	Text      string
	Timestamp time.Time
}
