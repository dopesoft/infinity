// Surface tools - the generic dashboard SURFACE CONTRACT, agent-facing.
//
// Rule #1 substrate. These two tools ARE the boundary the LLM assembles
// against when it wants to put something in front of the boss. A triage
// skill that pulls email, ranks it, and "drops the important ones on the
// dashboard" does that last step through surface_item - not through a
// bespoke table, not through Go. Anything the agent surfaces this way
// renders generically in Studio with zero new widget code.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// FollowupBodyFetcher retrieves a message's full rendered body (HTML + text)
// for durable capture at surface time. Satisfied by connectors.MessageFetcher
// and injected (late-bound in serve.go) so the generic surface tool stays
// vendor-agnostic - it asks "give me the body for this id", never "call Gmail".
type FollowupBodyFetcher interface {
	FetchMessage(ctx context.Context, source, accountHint, messageID string) (html, text string, attachments []connectors.Attachment, err error)
}

// RegisterSurfaceTools wires surface_item + surface_update + surface_list.
// No-op when pool is nil so chat-only / no-DB deployments don't break
// registration.
func RegisterSurfaceTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	store := surface.NewStore(pool, nil)
	r.Register(&surfaceItemTool{store: store})
	r.Register(&surfaceUpdateTool{store: store})
	r.Register(&surfaceListTool{store: store})
}

// ── surface_item ────────────────────────────────────────────────────────────

type surfaceItemTool struct {
	store   *surface.Store
	fetcher FollowupBodyFetcher // late-bound; nil until serve.go wires it
}

// SetBodyFetcher injects the durable-body fetcher after registration (the
// fetcher is constructed later in boot than the tool registry).
func (t *surfaceItemTool) SetBodyFetcher(f FollowupBodyFetcher) { t.fetcher = f }

func (t *surfaceItemTool) Name() string { return "surface_item" }
func (t *surfaceItemTool) Description() string {
	return "Put a ranked, structured item on the boss's dashboard. This is the " +
		"standard contract for surfacing ANYTHING - an important email a triage " +
		"recipe found, an alert, a digest entry, an insight. Choose the `surface` " +
		"by WHO the item is for and what it is:\n" +
		"  • 'followups' (aliases 'inbox'/'email') = a MESSAGE A PERSON is waiting " +
		"on the boss to act on (email, DM, mention). MESSAGES ONLY - never your " +
		"own notes. Renders in the Follow-ups card.\n" +
		"  • 'system' = YOUR OWN operational/status note about your own work " +
		"(a blocker, a failure, a completion, an observation - e.g. 'inbox triage " +
		"blocked on primary Gmail'). NOT a message. Renders in the Activity log.\n" +
		"  • 'alerts' / 'insights' / 'digest' = something for the boss to READ or " +
		"DECIDE on (a flagged finding, a summary). Renders in the Surfaced card.\n" +
		"Rule of thumb: if it is not a message from a person awaiting a reply, it " +
		"does NOT go in 'followups'. Set `kind` (icon: 'email','message','alert'," +
		"'article','metric','event','finding'). Set `importance` 0-100 when judged " +
		"- the dashboard floats high-importance items to the top. Pass `external_id` " +
		"(e.g. a Gmail message id) so re-running the same recipe refreshes the row " +
		"instead of duplicating it. Put extra structured payload in `metadata`. " +
		"Returns the item id."
}
func (t *surfaceItemTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"surface":           map[string]any{"type": "string", "description": "Dashboard region. 'followups' = a message a PERSON awaits a reply on (Follow-ups card, messages ONLY). 'system' = your own operational/status note (Activity log). 'alerts'/'insights'/'digest' = something to read/decide (Surfaced card). Never put your own notes in 'followups'."},
			"title":             map[string]any{"type": "string", "description": "Headline shown on the card. Required."},
			"kind":              map[string]any{"type": "string", "description": "Semantic type for the icon: 'email','message','alert','article','metric','event','finding'. Default 'item'."},
			"source":            map[string]any{"type": "string", "description": "Who produced this - your skill name, a connector slug, a cron name. Default 'agent'."},
			"external_id":       map[string]any{"type": "string", "description": "Stable id from the source system (Gmail message id, Slack ts, …). Enables upsert-on-rerun."},
			"subtitle":          map[string]any{"type": "string", "description": "Secondary line under the title."},
			"body":              map[string]any{"type": "string", "description": "Full content shown when the boss expands the item."},
			"body_html":         map[string]any{"type": "string", "description": "For email/message follow-ups: the FULL rendered HTML body, fetched once now (e.g. GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID format=full). Persisted so the boss can read the whole email from the dashboard WITHOUT a live connector call - durable even if the account is later revoked. Always pass this when surfacing an email so it never goes blank."},
			"body_text":         map[string]any{"type": "string", "description": "Plain-text fallback body for an email/message follow-up, persisted alongside body_html for the same durability. Pass when you have it."},
			"url":               map[string]any{"type": "string", "description": "Deep link to the source (thread URL, article URL)."},
			"importance":        map[string]any{"type": "integer", "description": "0-100 ranking. Omit if you haven't judged it. 80+ = urgent, 50-79 = notable, <50 = routine."},
			"importance_reason": map[string]any{"type": "string", "description": "One line explaining the importance score."},
			"metadata":          map[string]any{"type": "object", "description": "Arbitrary structured payload (from, attachments, draft, …). Rendered in the ObjectViewer and readable by downstream skills."},
			"expires_in_hours":  map[string]any{"type": "number", "description": "Optional TTL - the item auto-dismisses after this many hours. Use for ephemera like a daily digest entry."},
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
		CachedHTML:       strString(in, "body_html"),
		CachedText:       strString(in, "body_text"),
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

	// Durable email body: for a follow-up email surfaced with a stable id and
	// no body already supplied, fetch + store the full rendered body NOW, while
	// the connector is healthy. The boss can then read the whole email from the
	// dashboard later with NO live connector call - even if the account is
	// revoked before he ever opens it. The fetch + MIME decode run in Go
	// (connectors.MessageFetcher), never through the model. Best-effort: a miss
	// just leaves the lazy open-time path (which also caches) to fill it later.
	if it.CachedHTML == "" && it.CachedText == "" && t.fetcher != nil &&
		it.ExternalID != "" && isFollowupEmailItem(it.Surface, it.Kind) {
		if html, text, _, ferr := t.fetcher.FetchMessage(ctx, it.Source, metaAccountHint(it.Metadata), it.ExternalID); ferr == nil {
			it.CachedHTML = html
			it.CachedText = text
		}
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

// isFollowupEmailItem reports whether a surface item is a follow-up email -
// the boss-owned class that no autonomous turn may resolve. Mirrors the
// dashboard read filter (surface ∈ {followups,inbox,email} AND kind='email')
// so the guard and the render agree on exactly which rows are protected.
func isFollowupEmailItem(surfaceName, kind string) bool {
	switch surfaceName {
	case "followups", "inbox", "email":
		return kind == "email"
	}
	return false
}

// metaAccountHint pulls the connector account hint a producer stashed in
// metadata (a ca_ connected_account_id, an email, or an entity label) so the
// body fetcher can resolve which account to read from. Empty when absent.
func metaAccountHint(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, k := range []string{"connected_account_id", "account"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ── surface_update ──────────────────────────────────────────────────────────

type surfaceUpdateTool struct{ store *surface.Store }

func (t *surfaceUpdateTool) Name() string { return "surface_update" }
func (t *surfaceUpdateTool) Description() string {
	return "Update a dashboard item you previously surfaced: mark status='done' " +
		"only after the surfaced issue is actually resolved or confirmed handled, " +
		"dismiss it when the boss declines it, re-rank its importance, or snooze it. " +
		"Pass the item `id` returned by surface_item. " +
		"NEVER resolve a follow-up EMAIL (surface 'followups'/'inbox'/'email', kind " +
		"'email') yourself - those are the boss's to disposition. Drafting a reply is " +
		"NOT 'handled'; leave the follow-up open until the boss sends. On unattended " +
		"runs this is enforced and will error."
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
		// Guard: an AUTONOMOUS turn (cron/heartbeat/sub-agent) may NOT
		// resolve a follow-up EMAIL. The boss dispositions his own inbox -
		// either in the UI or by an explicit request in live chat. This is
		// the durable fix for the incident where a scheduled triage run
		// dismissed days of real follow-ups (Amex, Intuit, …) on its own.
		// Interactive turns are unaffected (IsAutonomous is false there).
		if (st == surface.StatusDone || st == surface.StatusDismissed) && IsAutonomous(ctx) {
			if it, gerr := t.store.Get(ctx, id); gerr == nil && it != nil && isFollowupEmailItem(it.Surface, it.Kind) {
				return "", fmt.Errorf("refusing to auto-%s a follow-up email (%q) on an unattended turn: the boss dispositions his own follow-ups. Leave it open - surface a 'system' note if you think it's handled, but never resolve a follow-up email yourself", st, it.Title)
			}
		}
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

// ── surface_list ────────────────────────────────────────────────────────────
//
// Without this, the agent has no way to enumerate dashboard items by id,
// so a request like "delete all the remaining questions" forces it to
// ask the boss to copy/paste IDs that aren't even shown in the UI. With
// surface_list, the agent can: list → identify by surface/kind/title →
// loop surface_update({id, status:"dismissed"}) over each. Read-only,
// safe to register everywhere.

type surfaceListTool struct{ store *surface.Store }

func (t *surfaceListTool) Name() string   { return "surface_list" }
func (t *surfaceListTool) ReadOnly() bool { return true }
func (t *surfaceListTool) Description() string {
	return "List items currently on the boss's dashboard with their ids. Use " +
		"this BEFORE surface_update when the boss asks to dismiss, snooze, or " +
		"re-rank items - you need the actual item ids and they are not shown " +
		"in the UI. Optional `surface` filter (e.g. 'questions', 'followups', " +
		"'alerts', 'digest', 'insights') narrows to one dashboard region; " +
		"omit it to see everything open across surfaces. Returns id, surface, " +
		"kind, title, subtitle, importance, status, created_at."
}
func (t *surfaceListTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"surface": map[string]any{
				"type":        "string",
				"description": "Optional surface name to filter by (e.g. 'questions'). Omit to list across all surfaces.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max items to return (default 100, max 500).",
				"default":     100,
			},
		},
	}
}
func (t *surfaceListTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	surfaceName := strString(in, "surface")
	limit := 100
	if v, ok := in["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 500 {
		limit = 500
	}

	var (
		items []*surface.Item
		err   error
	)
	if surfaceName != "" {
		items, err = t.store.ListBySurface(ctx, surfaceName, limit)
	} else {
		items, err = t.store.ListOpen(ctx, limit)
	}
	if err != nil {
		return "", fmt.Errorf("surface_list: %w", err)
	}

	type row struct {
		ID         string `json:"id"`
		Surface    string `json:"surface"`
		Kind       string `json:"kind"`
		Title      string `json:"title"`
		Subtitle   string `json:"subtitle,omitempty"`
		Importance *int   `json:"importance,omitempty"`
		Status     string `json:"status"`
		CreatedAt  string `json:"created_at"`
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		r := row{
			ID:        it.ID,
			Surface:   it.Surface,
			Kind:      it.Kind,
			Title:     it.Title,
			Subtitle:  it.Subtitle,
			Status:    string(it.Status),
			CreatedAt: it.CreatedAt.UTC().Format(time.RFC3339),
		}
		if it.Importance != nil {
			v := *it.Importance
			r.Importance = &v
		}
		rows = append(rows, r)
	}
	out, _ := json.Marshal(map[string]any{
		"count": len(rows),
		"items": rows,
	})
	return string(out), nil
}
