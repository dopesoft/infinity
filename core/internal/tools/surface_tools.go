// Surface tools — the generic dashboard SURFACE CONTRACT, agent-facing.
//
// Rule #1 substrate. These two tools ARE the boundary the LLM assembles
// against when it wants to put something in front of the boss. A triage
// skill that pulls email, ranks it, and "drops the important ones on the
// dashboard" does that last step through surface_item — not through a
// bespoke table, not through Go. Anything the agent surfaces this way
// renders generically in Studio with zero new widget code.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/surface"
)

// RegisterSurfaceTools wires surface_item + surface_update. No-op when
// pool is nil so chat-only / no-DB deployments don't break registration.
func RegisterSurfaceTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	store := surface.NewStore(pool, nil)
	r.Register(&surfaceItemTool{store: store})
	r.Register(&surfaceUpdateTool{store: store})
}

// ── surface_item ────────────────────────────────────────────────────────────

type surfaceItemTool struct{ store *surface.Store }

func (t *surfaceItemTool) Name() string { return "surface_item" }
func (t *surfaceItemTool) Description() string {
	return "Put a ranked, structured item on the boss's dashboard. This is the " +
		"standard contract for surfacing ANYTHING — an important email a triage " +
		"recipe found, an alert, a digest entry, an insight. Pick a `surface` (the " +
		"dashboard region: 'followups', 'alerts', 'digest', 'insights', or invent " +
		"one) and a `kind` (semantic type for the icon: 'email', 'message', " +
		"'alert', 'article', 'metric', 'event', 'finding'). Set `importance` 0-100 " +
		"when you've judged it — the dashboard floats high-importance items to the " +
		"top. Pass `external_id` (e.g. a Gmail message id) so re-running the same " +
		"recipe refreshes the row instead of duplicating it. Put any extra " +
		"structured payload in `metadata`. Returns the item id."
}
func (t *surfaceItemTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"surface":           map[string]any{"type": "string", "description": "Dashboard region: 'followups', 'alerts', 'digest', 'insights', … (free-form)."},
			"title":             map[string]any{"type": "string", "description": "Headline shown on the card. Required."},
			"kind":              map[string]any{"type": "string", "description": "Semantic type for the icon: 'email','message','alert','article','metric','event','finding'. Default 'item'."},
			"source":            map[string]any{"type": "string", "description": "Who produced this — your skill name, a connector slug, a cron name. Default 'agent'."},
			"external_id":       map[string]any{"type": "string", "description": "Stable id from the source system (Gmail message id, Slack ts, …). Enables upsert-on-rerun."},
			"subtitle":          map[string]any{"type": "string", "description": "Secondary line under the title."},
			"body":              map[string]any{"type": "string", "description": "Full content shown when the boss expands the item."},
			"url":               map[string]any{"type": "string", "description": "Deep link to the source (thread URL, article URL)."},
			"importance":        map[string]any{"type": "integer", "description": "0-100 ranking. Omit if you haven't judged it. 80+ = urgent, 50-79 = notable, <50 = routine."},
			"importance_reason": map[string]any{"type": "string", "description": "One line explaining the importance score."},
			"metadata":          map[string]any{"type": "object", "description": "Arbitrary structured payload (from, attachments, draft, …). Rendered in the ObjectViewer and readable by downstream skills."},
			"expires_in_hours":  map[string]any{"type": "number", "description": "Optional TTL — the item auto-dismisses after this many hours. Use for ephemera like a daily digest entry."},
		},
		"required": []string{"surface", "title"},
	}
}
func (t *surfaceItemTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	it := &surface.Item{
		Surface:          strString(in, "surface"),
		Title:            strString(in, "title"),
		Kind:             strString(in, "kind"),
		Source:           strString(in, "source"),
		ExternalID:       strString(in, "external_id"),
		Subtitle:         strString(in, "subtitle"),
		Body:             strString(in, "body"),
		URL:              strString(in, "url"),
		ImportanceReason: strString(in, "importance_reason"),
	}
	if v, ok := in["importance"].(float64); ok {
		imp := int(v)
		it.Importance = &imp
	}
	if m, ok := in["metadata"].(map[string]any); ok {
		it.Metadata = m
	}
	if v, ok := in["expires_in_hours"].(float64); ok && v > 0 {
		exp := time.Now().UTC().Add(time.Duration(v * float64(time.Hour)))
		it.ExpiresAt = &exp
	}
	id, err := t.store.Upsert(ctx, it)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":      true,
		"id":      id,
		"surface": it.Surface,
		"kind":    it.Kind,
		"message": fmt.Sprintf("Surfaced %q on the %q dashboard region.", it.Title, it.Surface),
	})
	return string(out), nil
}

// ── surface_update ──────────────────────────────────────────────────────────

type surfaceUpdateTool struct{ store *surface.Store }

func (t *surfaceUpdateTool) Name() string { return "surface_update" }
func (t *surfaceUpdateTool) Description() string {
	return "Update a dashboard item you previously surfaced: dismiss it once it's " +
		"handled, re-rank its importance, or snooze it. Pass the item `id` returned " +
		"by surface_item."
}
func (t *surfaceUpdateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                map[string]any{"type": "string", "description": "The surface item id."},
			"status":            map[string]any{"type": "string", "enum": []string{"open", "snoozed", "done", "dismissed"}, "description": "New lifecycle state."},
			"importance":        map[string]any{"type": "integer", "description": "Re-rank 0-100."},
			"importance_reason": map[string]any{"type": "string", "description": "One line explaining the new score."},
			"snooze_hours":      map[string]any{"type": "number", "description": "Hide the item for this many hours (sets status=snoozed automatically)."},
		},
		"required": []string{"id"},
	}
}
func (t *surfaceUpdateTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id is required")
	}
	var p surface.Patch
	if s := strString(in, "status"); s != "" {
		st := surface.Status(s)
		p.Status = &st
	}
	if v, ok := in["importance"].(float64); ok {
		imp := int(v)
		p.Importance = &imp
	}
	if r := strString(in, "importance_reason"); r != "" {
		p.ImportanceReason = &r
	}
	if v, ok := in["snooze_hours"].(float64); ok && v > 0 {
		until := time.Now().UTC().Add(time.Duration(v * float64(time.Hour)))
		p.SnoozedUntil = &until
		snoozed := surface.StatusSnoozed
		p.Status = &snoozed
	}
	if err := t.store.Update(ctx, id, p); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "id": id})
	return string(out), nil
}
