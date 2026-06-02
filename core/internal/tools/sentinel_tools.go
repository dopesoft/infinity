// Sentinel tools - let Jarvis stand up event-driven watchers from chat.
//
// Five tools land here, mirroring the cron tools (cron_tools.go):
//
//   sentinel_create - create/update a watcher (the "ping me WHEN x" path)
//   sentinel_list   - list every sentinel
//   sentinel_delete - remove a sentinel by id or name
//   sentinel_pause  - toggle enabled without deleting
//   sentinel_test   - fire a sentinel once now to verify its action chain
//
// Why this exists: crons answer "do X at a TIME"; sentinels answer "do X WHEN
// an event happens". The agent already had cron_create_* but NO sentinel path,
// so the only way to make a watcher was hand-writing two blocks of JSON in the
// Studio form. That violated Rule #1 (the agent should ASSEMBLE the capability
// from a natural-language request). These tools close that gap: the boss says
// "ping me when unread email tops 20" and the model picks sentinel_create with
// watch_type=threshold + an action skill.
//
// Like the cron tools, these wrap the existing sentinel.Manager through a thin
// local interface + adapter (serve.go) so the tools package doesn't import the
// sentinel package.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SentinelRow is the minimal sentinel shape the tools need. Mirrors
// sentinel.Sentinel but lives here so tools doesn't import the sentinel
// package (matches the CronJob pattern).
type SentinelRow struct {
	ID              string
	Name            string
	WatchType       string
	WatchConfig     json.RawMessage
	ActionChain     json.RawMessage
	CooldownSeconds int
	Enabled         bool
	FireCount       int
	LastTriggeredAt *time.Time
}

// SentinelManager is the contract the tools rely on. The concrete type
// (sentinel.Manager) satisfies it through an adapter built in serve.go.
type SentinelManager interface {
	Upsert(ctx context.Context, s SentinelRow) (string, error)
	Delete(ctx context.Context, id string) error
	List() []SentinelRow
	Trigger(ctx context.Context, id string, payload map[string]any) error
}

// validWatchTypes mirrors sentinel.WatchType.Valid() so the tool can reject a
// bad type with a helpful message before hitting the manager.
var validWatchTypes = map[string]bool{
	"webhook":           true,
	"file_change":       true,
	"memory_event":      true,
	"external_api_poll": true,
	"threshold":         true,
	"goal_event":        true,
}

// watchConfigHelp documents the watch_config keys per type, inlined into the
// sentinel_create description so the model assembles the right shape without
// reading the runtime source.
const watchConfigHelp = `watch_config keys by type:
- threshold: {"sql":"SELECT count(*) FROM ...","operator":">=","value":20,"interval_seconds":300} (sql MUST be a single SELECT, no semicolons)
- memory_event: {"query":"text to match","hook_name":"","min_importance":0,"since_seconds":300,"interval_seconds":60}
- external_api_poll: {"url":"https://...","method":"GET","headers":{},"contains":"","fire_on_change":true,"interval_seconds":300}
- file_change: {"path":"/abs/path","interval_seconds":60}
- goal_event: {"goal_id":"","entity_id":"","on_status_in":["blocked"],"interval_seconds":60}
- webhook: {} (no polling; an external system POSTs to /api/sentinels/{id}/trigger)`

// RegisterSentinelTools wires every sentinel management tool. No-op when mgr is
// nil so chat-only / no-DB deployments don't break tool registration.
func RegisterSentinelTools(r *Registry, mgr SentinelManager) {
	if r == nil || mgr == nil {
		return
	}
	r.Register(&sentinelCreate{mgr: mgr})
	r.Register(&sentinelList{mgr: mgr})
	r.Register(&sentinelDelete{mgr: mgr})
	r.Register(&sentinelPause{mgr: mgr})
	r.Register(&sentinelTest{mgr: mgr})
}

// ── sentinel_create ─────────────────────────────────────────────────────────

type sentinelCreate struct{ mgr SentinelManager }

func (t *sentinelCreate) Name() string { return "sentinel_create" }
func (t *sentinelCreate) Description() string {
	return "Create an event-driven watcher (a 'sentinel'). Use this when the boss wants something to happen WHEN a condition occurs, not at a fixed time (that's cron_create_agent). When it trips, it runs `action_prompt` as an agent turn — you carry out that instruction with full tool access (notify, surface_item, skills_invoke, …), so 'ping me when X' just works. Reuses the name to update in place.\n\n" + watchConfigHelp
}
func (t *sentinelCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":             map[string]any{"type": "string", "description": "Unique name (kebab-case). Reused to update an existing sentinel."},
			"watch_type":       map[string]any{"type": "string", "enum": []string{"webhook", "file_change", "memory_event", "external_api_poll", "threshold", "goal_event"}, "description": "What kind of event to watch for."},
			"watch_config":     map[string]any{"type": "object", "description": "Type-specific config. See the key reference in this tool's description."},
			"action_prompt":    map[string]any{"type": "string", "description": "PRIMARY action: a natural-language instruction the agent carries out when the sentinel fires, with full tool access. The trigger detail (what tripped) is appended automatically. For a notification, phrase it like 'Notify the boss that unread email topped 20.' — the agent will use the notify tool."},
			"action_skill":     map[string]any{"type": "string", "description": "Convenience: a skill slug to run on fire (wrapped as an agent turn that runs the skill — works for every skill type). Use action_prompt instead when the goal is just to notify or do something ad-hoc."},
			"action_args":      map[string]any{"type": "object", "description": "Optional args mentioned to the agent when using action_skill."},
			"action_chain":     map[string]any{"type": "array", "description": "Advanced: raw action chain ([{\"kind\":\"agent_turn\",\"args\":{\"prompt\":\"...\"}}] or [{\"kind\":\"skill\",\"args\":{\"name\":\"...\",\"args\":{}}}] for an executable code skill). Overrides action_prompt/action_skill when given."},
			"cooldown_seconds": map[string]any{"type": "integer", "description": "Minimum seconds between fires (default 300). Stops it spamming on a sticky condition."},
		},
		"required": []string{"name", "watch_type"},
	}
}
func (t *sentinelCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	name := strString(in, "name")
	wt := strings.TrimSpace(strString(in, "watch_type"))
	if name == "" || wt == "" {
		return "", errors.New("name and watch_type are required")
	}
	if !validWatchTypes[wt] {
		return "", fmt.Errorf("invalid watch_type %q (one of: webhook, file_change, memory_event, external_api_poll, threshold, goal_event)", wt)
	}

	// watch_config: pass through the object the model assembled.
	var cfgJSON json.RawMessage = []byte("{}")
	if cfg, ok := in["watch_config"].(map[string]any); ok && len(cfg) > 0 {
		b, err := json.Marshal(cfg)
		if err != nil {
			return "", fmt.Errorf("encode watch_config: %w", err)
		}
		cfgJSON = b
	}

	// action_chain: prefer the raw chain if given, else build one from the
	// action_skill convenience (the common case). A sentinel with no action
	// is useless, so require one or the other.
	chainJSON, err := buildActionChain(in)
	if err != nil {
		return "", err
	}

	cooldown := intOr(in, "cooldown_seconds", 300)

	id, err := t.mgr.Upsert(ctx, SentinelRow{
		Name:            name,
		WatchType:       wt,
		WatchConfig:     cfgJSON,
		ActionChain:     chainJSON,
		CooldownSeconds: cooldown,
		Enabled:         true,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","name":"%s","watch_type":"%s"}`, id, name, wt), nil
}

// buildActionChain returns the action_chain JSON for the sentinel. Priority:
//
//  1. raw `action_chain` (power users / code skills) — used verbatim.
//  2. `action_prompt` — wrapped as an "agent_turn" action: when the sentinel
//     fires, the agent carries out this natural-language instruction with full
//     tool access (notify, surface_item, skills_invoke, …). This is the path
//     for "ping me when X" and the default the model should reach for.
//  3. `action_skill` — also wrapped as an "agent_turn" ("Run your `X` skill
//     now"), NOT a raw "skill" action, because the agent loop runs every skill
//     type (including instruction-only recipes) whereas the direct runner path
//     only executes code skills.
//
// Errors if none is present (an actionless sentinel does nothing useful).
func buildActionChain(in map[string]any) (json.RawMessage, error) {
	if raw, ok := in["action_chain"].([]any); ok && len(raw) > 0 {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("encode action_chain: %w", err)
		}
		return b, nil
	}

	var prompt string
	if p := strString(in, "action_prompt"); p != "" {
		prompt = p
	} else if skill := strString(in, "action_skill"); skill != "" {
		prompt = fmt.Sprintf("Run your `%s` skill now and act on what it surfaces.", skill)
		if a, ok := in["action_args"].(map[string]any); ok && len(a) > 0 {
			if aj, err := json.Marshal(a); err == nil {
				prompt += " Skill args: " + string(aj)
			}
		}
	}
	if prompt == "" {
		return nil, errors.New("give the sentinel something to do: set action_prompt (a natural-language instruction the agent runs when it fires, e.g. \"Notify the boss that ...\"), action_skill (a skill to run), or a raw action_chain")
	}

	chain := []map[string]any{
		{"kind": "agent_turn", "args": map[string]any{"prompt": prompt}},
	}
	b, err := json.Marshal(chain)
	if err != nil {
		return nil, fmt.Errorf("encode action chain: %w", err)
	}
	return b, nil
}

// ── sentinel_list ─────────────────────────────────────────────────────────

type sentinelList struct{ mgr SentinelManager }

func (t *sentinelList) Name() string { return "sentinel_list" }
func (t *sentinelList) Description() string {
	return "List every sentinel (event watcher). Returns id, name, watch_type, enabled, fire_count."
}
func (t *sentinelList) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *sentinelList) Execute(_ context.Context, _ map[string]any) (string, error) {
	list := t.mgr.List()
	type row struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		WatchType string `json:"watch_type"`
		Enabled   bool   `json:"enabled"`
		FireCount int    `json:"fire_count"`
	}
	out := make([]row, 0, len(list))
	for _, s := range list {
		out = append(out, row{ID: s.ID, Name: s.Name, WatchType: s.WatchType, Enabled: s.Enabled, FireCount: s.FireCount})
	}
	b, _ := json.Marshal(map[string]any{"sentinels": out})
	return string(b), nil
}

// ── sentinel_delete ─────────────────────────────────────────────────────────

type sentinelDelete struct{ mgr SentinelManager }

func (t *sentinelDelete) Name() string { return "sentinel_delete" }
func (t *sentinelDelete) Description() string {
	return "Delete a sentinel by id OR name. Returns the resolved id deleted."
}
func (t *sentinelDelete) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "string", "description": "UUID. Optional if name is given."},
			"name": map[string]any{"type": "string", "description": "Unique name. Optional if id is given."},
		},
	}
}
func (t *sentinelDelete) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, err := resolveSentinelID(t.mgr, in)
	if err != nil {
		return "", err
	}
	if err := t.mgr.Delete(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s"}`, id), nil
}

// ── sentinel_pause ─────────────────────────────────────────────────────────

type sentinelPause struct{ mgr SentinelManager }

func (t *sentinelPause) Name() string { return "sentinel_pause" }
func (t *sentinelPause) Description() string {
	return "Pause or resume a sentinel. enabled=false stops it firing without deleting; enabled=true resumes."
}
func (t *sentinelPause) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"name":    map[string]any{"type": "string"},
			"enabled": map[string]any{"type": "boolean", "description": "false = pause, true = resume."},
		},
		"required": []string{"enabled"},
	}
}
func (t *sentinelPause) Execute(ctx context.Context, in map[string]any) (string, error) {
	enabled, ok := in["enabled"].(bool)
	if !ok {
		return "", errors.New("enabled (boolean) required")
	}
	// Re-upsert the row with the flipped flag, preserving its config/actions.
	// Upsert is ON CONFLICT(name) so it updates in place — no raw SQL needed.
	cur, err := findSentinel(t.mgr, in)
	if err != nil {
		return "", err
	}
	id, err := t.mgr.Upsert(ctx, SentinelRow{
		Name:            cur.Name,
		WatchType:       cur.WatchType,
		WatchConfig:     cur.WatchConfig,
		ActionChain:     cur.ActionChain,
		CooldownSeconds: cur.CooldownSeconds,
		Enabled:         enabled,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","enabled":%v}`, id, enabled), nil
}

// ── sentinel_test ─────────────────────────────────────────────────────────

type sentinelTest struct{ mgr SentinelManager }

func (t *sentinelTest) Name() string { return "sentinel_test" }
func (t *sentinelTest) Description() string {
	return "Fire a sentinel once right now to verify its action chain runs. Respects cooldown + enabled state. Use after sentinel_create to confirm the setup works."
}
func (t *sentinelTest) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"name":    map[string]any{"type": "string"},
			"payload": map[string]any{"type": "object", "description": "Optional test payload passed to the action chain."},
		},
	}
}
func (t *sentinelTest) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, err := resolveSentinelID(t.mgr, in)
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if p, ok := in["payload"].(map[string]any); ok {
		payload = p
	} else {
		payload = map[string]any{"__test": true}
	}
	if err := t.mgr.Trigger(ctx, id, payload); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","fired":true}`, id), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

// findSentinel resolves a sentinel from the in-memory list by id or name.
func findSentinel(mgr SentinelManager, in map[string]any) (SentinelRow, error) {
	id := strString(in, "id")
	name := strString(in, "name")
	if id == "" && name == "" {
		return SentinelRow{}, errors.New("id or name required")
	}
	for _, s := range mgr.List() {
		if (id != "" && s.ID == id) || (name != "" && s.Name == name) {
			return s, nil
		}
	}
	return SentinelRow{}, fmt.Errorf("sentinel not found (id=%q, name=%q)", id, name)
}

func resolveSentinelID(mgr SentinelManager, in map[string]any) (string, error) {
	s, err := findSentinel(mgr, in)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// intOr reads an int-ish value (JSON numbers decode as float64) with a default.
func intOr(in map[string]any, key string, def int) int {
	switch v := in[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}
