// tools.go - agent-facing calendar tools.
//
// Jarvis acts on the calendar through these tools (NOT through raw
// composio__GOOGLECALENDAR_* verbs). Three reasons:
//
//   1. Identity: the boss has the local UUID in front of him in chat
//      ("the 3pm with M"); these tools resolve it to (account_id,
//      calendar_id, remote_id) automatically. The raw Composio verbs
//      require the boss to know the remote google event id.
//
//   2. Coherence: write-then-resync. After a respond/patch/create we
//      kick a sync tick so the dashboard reflects the change in
//      seconds, not in 2 minutes.
//
//   3. One shape: same Provider interface that the ticker + the HTTP
//      Accept/Decline buttons use, so behaviour is identical across
//      every entry point.
//
// Registered from serve.go via calendar.RegisterTools(registry, syncer).

package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires every calendar agent tool. No-op when syncer is
// nil so chat-only / no-DB deployments don't break tool registration.
func RegisterTools(reg *tools.Registry, syncer *Syncer) {
	if reg == nil || syncer == nil {
		return
	}
	reg.Register(&toolRespond{syncer: syncer})
	reg.Register(&toolSyncNow{syncer: syncer})
	reg.Register(&toolCreate{syncer: syncer})
	reg.Register(&toolPatch{syncer: syncer})
	reg.Register(&toolDelete{syncer: syncer})
}

// ── calendar_respond ─────────────────────────────────────────────────────

type toolRespond struct{ syncer *Syncer }

func (t *toolRespond) Name() string { return "calendar_respond" }
func (t *toolRespond) Description() string {
	return "Set the boss's RSVP on a calendar event he was invited to. `event_id` is the local mem_calendar_events row id (the Upcoming card uses this). `response` is one of accepted | declined | tentative. The change writes through to Google immediately and a sync tick fires to refresh local state."
}
func (t *toolRespond) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"event_id": map[string]any{"type": "string", "description": "Local mem_calendar_events.id (UUID)."},
			"response": map[string]any{"type": "string", "enum": []string{"accepted", "declined", "tentative"}, "description": "Boss's response."},
		},
		"required": []string{"event_id", "response"},
	}
}
func (t *toolRespond) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, _ := in["event_id"].(string)
	resp, _ := in["response"].(string)
	if id == "" || resp == "" {
		return "", errors.New("event_id and response are required")
	}
	ev, err := t.syncer.Respond(ctx, id, Response(resp))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "event": ev})
	return string(out), nil
}

// ── calendar_sync_now ────────────────────────────────────────────────────

type toolSyncNow struct{ syncer *Syncer }

func (t *toolSyncNow) Name() string { return "calendar_sync_now" }
func (t *toolSyncNow) Description() string {
	return "Force an immediate calendar sync tick for the given Composio account. Use after creating/patching an event in another client, or when the boss says 'my calendar looks stale'. Default cadence is every 2 min so manual sync is only needed for impatience."
}
func (t *toolSyncNow) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id":  map[string]any{"type": "string", "description": "Composio connected_account_id (ca_xxx)."},
			"calendar_id": map[string]any{"type": "string", "description": "Google calendarId, defaults to 'primary'."},
		},
		"required": []string{"account_id"},
	}
}
func (t *toolSyncNow) Execute(ctx context.Context, in map[string]any) (string, error) {
	acc, _ := in["account_id"].(string)
	cal, _ := in["calendar_id"].(string)
	if acc == "" {
		return "", errors.New("account_id required")
	}
	res := t.syncer.SyncNow(ctx, acc, cal)
	out, _ := json.Marshal(res)
	return string(out), nil
}

// ── calendar_create ──────────────────────────────────────────────────────

type toolCreate struct{ syncer *Syncer }

func (t *toolCreate) Name() string { return "calendar_create" }
func (t *toolCreate) Description() string {
	return "Create a new calendar event. `body` is a Google Calendar Event resource (summary, start, end, description, location, attendees, conferenceData...). The agent should build the body in Google's native shape - timezone-aware dateTimes preferred. After creation a sync tick fires so the new event lands in mem_calendar_events with a local UUID."
}
func (t *toolCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id":  map[string]any{"type": "string", "description": "Composio connected_account_id (ca_xxx)."},
			"calendar_id": map[string]any{"type": "string", "description": "Google calendarId, defaults to 'primary'."},
			"body":        map[string]any{"type": "object", "description": "Google Event resource body."},
		},
		"required": []string{"account_id", "body"},
	}
}
func (t *toolCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	acc, _ := in["account_id"].(string)
	cal, _ := in["calendar_id"].(string)
	body, _ := in["body"].(map[string]any)
	if acc == "" || body == nil {
		return "", errors.New("account_id and body required")
	}
	ev, err := t.syncer.provider.Create(ctx, acc, cal, body)
	if err != nil {
		return "", err
	}
	if err := t.syncer.store.UpsertEvent(ctx, ev); err != nil {
		return "", fmt.Errorf("upsert: %w", err)
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "event": ev})
	return string(out), nil
}

// ── calendar_patch ───────────────────────────────────────────────────────

type toolPatch struct{ syncer *Syncer }

func (t *toolPatch) Name() string { return "calendar_patch" }
func (t *toolPatch) Description() string {
	return "Apply a partial update to an existing calendar event. `event_id` is the local mem_calendar_events row id; `patch` is a Google Event resource fragment (only the fields to change). For RSVPs use calendar_respond instead."
}
func (t *toolPatch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"event_id": map[string]any{"type": "string", "description": "Local mem_calendar_events.id (UUID)."},
			"patch":    map[string]any{"type": "object", "description": "Google Event partial body."},
		},
		"required": []string{"event_id", "patch"},
	}
}
func (t *toolPatch) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, _ := in["event_id"].(string)
	patch, _ := in["patch"].(map[string]any)
	if id == "" || patch == nil {
		return "", errors.New("event_id and patch required")
	}
	acc, cal, remote, err := t.syncer.store.FindEvent(ctx, id)
	if err != nil {
		return "", err
	}
	ev, err := t.syncer.provider.Patch(ctx, acc, cal, remote, patch)
	if err != nil {
		return "", err
	}
	if err := t.syncer.store.UpsertEvent(ctx, ev); err != nil {
		return "", fmt.Errorf("upsert: %w", err)
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "event": ev})
	return string(out), nil
}

// ── calendar_delete ──────────────────────────────────────────────────────

type toolDelete struct{ syncer *Syncer }

func (t *toolDelete) Name() string { return "calendar_delete" }
func (t *toolDelete) Description() string {
	return "Cancel/delete a calendar event the boss organizes. For events he was just invited to, use calendar_respond with response='declined' instead. `event_id` is the local mem_calendar_events row id."
}
func (t *toolDelete) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"event_id": map[string]any{"type": "string", "description": "Local mem_calendar_events.id (UUID)."},
		},
		"required": []string{"event_id"},
	}
}
func (t *toolDelete) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, _ := in["event_id"].(string)
	if id == "" {
		return "", errors.New("event_id required")
	}
	acc, cal, remote, err := t.syncer.store.FindEvent(ctx, id)
	if err != nil {
		return "", err
	}
	if err := t.syncer.provider.Delete(ctx, acc, cal, remote); err != nil {
		return "", err
	}
	// Best-effort local clean up; next sync would do this too.
	_, _ = t.syncer.store.pool.Exec(ctx, `DELETE FROM mem_calendar_events WHERE id = $1`, id)
	out, _ := json.Marshal(map[string]any{"ok": true})
	return string(out), nil
}
