// Package sentinel implements Phase 6 event-driven watchers — webhooks,
// file-change watchers, memory-event triggers, external HTTP polls, and
// metric thresholds.
package sentinel

import (
	"encoding/json"
	"time"
)

type WatchType string

const (
	WatchWebhook       WatchType = "webhook"
	WatchFileChange    WatchType = "file_change"
	WatchMemoryEvent   WatchType = "memory_event"
	WatchExternalPoll  WatchType = "external_api_poll"
	WatchThreshold     WatchType = "threshold"
)

func (w WatchType) Valid() bool {
	switch w {
	case WatchWebhook, WatchFileChange, WatchMemoryEvent, WatchExternalPoll, WatchThreshold:
		return true
	}
	return false
}

// Sentinel is the row shape from mem_sentinels.
type Sentinel struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	WatchType       WatchType       `json:"watch_type"`
	WatchConfig     json.RawMessage `json:"watch_config"`
	ActionChain     json.RawMessage `json:"action_chain"`
	CooldownSeconds int             `json:"cooldown_seconds"`
	LastTriggeredAt *time.Time      `json:"last_triggered_at,omitempty"`
	FireCount       int             `json:"fire_count"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"created_at"`
}

// Action is one entry in action_chain. The watcher fires the chain in order;
// failures abort subsequent entries.
type Action struct {
	Kind string         `json:"kind"` // "skill" | "memory_write" | "notification"
	Args map[string]any `json:"args"`
}
