package hooks

import "time"

// EventName enumerates the 12 hook events listed in the spec (PDF p.19).
type EventName string

const (
	SessionStart        EventName = "SessionStart"
	UserPromptSubmit    EventName = "UserPromptSubmit"
	PreToolUse          EventName = "PreToolUse"
	PostToolUse         EventName = "PostToolUse"
	PostToolUseFailure  EventName = "PostToolUseFailure"
	PreCompact          EventName = "PreCompact"
	SubagentStart       EventName = "SubagentStart"
	SubagentStop        EventName = "SubagentStop"
	Notification        EventName = "Notification"
	TaskCompleted       EventName = "TaskCompleted"
	Stop                EventName = "Stop"
	SessionEnd          EventName = "SessionEnd"
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
