package worldmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires the seven agent-facing world-model tools:
//
//	entity_upsert  - add/update a person/project/account/… in the world model
//	entity_link    - connect two entities with a typed relation
//	entity_get     - read one entity + its links
//	entity_search  - find entities by kind / text
//	goal_set       - create or replace one of the agent's own goals
//	goal_update    - record progress / re-plan / mark blocked
//	goal_list      - list the agent's goals
//
// Lives in package worldmodel (which imports tools) - no import cycle.
func RegisterTools(reg *tools.Registry, store *Store) {
	if reg == nil || store == nil {
		return
	}
	reg.Register(&entityUpsertTool{store: store})
	reg.Register(&entityLinkTool{store: store})
	reg.Register(&entityGetTool{store: store})
	reg.Register(&entitySearchTool{store: store})
	reg.Register(&goalSetTool{store: store})
	reg.Register(&goalUpdateTool{store: store})
	reg.Register(&goalListTool{store: store})
}

// ── entity_upsert ───────────────────────────────────────────────────────────

type entityUpsertTool struct{ store *Store }

func (t *entityUpsertTool) Name() string { return "entity_upsert" }
func (t *entityUpsertTool) Description() string {
	return "Add or update an entity in the world model - a person, project, " +
		"account, org, thread, commitment, or any other thing in the boss's " +
		"world. Use this whenever you learn a durable fact about who/what the " +
		"boss works with. `attributes` is free-form structured facts (role, " +
		"email, status, repo, …) and is MERGED on update, not replaced. " +
		"`salience` (0-100) is how central this is to the boss right now."
}
func (t *entityUpsertTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":       map[string]any{"type": "string", "description": "person | project | account | org | thread | commitment | place | … (free-form)."},
			"name":       map[string]any{"type": "string", "description": "Canonical name."},
			"aliases":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Other names this entity goes by, for resolution."},
			"attributes": map[string]any{"type": "object", "description": "Structured facts - merged into existing attributes on update."},
			"summary":    map[string]any{"type": "string", "description": "One-paragraph current-state summary."},
			"salience":   map[string]any{"type": "integer", "description": "0-100: how central to the boss's world right now. Default 50."},
			"status":     map[string]any{"type": "string", "enum": []string{"active", "dormant", "archived"}},
		},
		"required": []string{"kind", "name"},
	}
}
func (t *entityUpsertTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	e := &Entity{
		Kind:    wmStr(in, "kind"),
		Name:    wmStr(in, "name"),
		Summary: wmStr(in, "summary"),
		Status:  wmStr(in, "status"),
	}
	if v, ok := in["salience"].(float64); ok {
		e.Salience = int(v)
	}
	if raw, ok := in["aliases"].([]any); ok {
		for _, a := range raw {
			if s, ok := a.(string); ok {
				e.Aliases = append(e.Aliases, s)
			}
		}
	}
	if m, ok := in["attributes"].(map[string]any); ok {
		e.Attributes = m
	}
	id, err := t.store.UpsertEntity(ctx, e)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "id": id, "kind": e.Kind, "name": e.Name})
	return string(out), nil
}

// ── entity_link ─────────────────────────────────────────────────────────────

type entityLinkTool struct{ store *Store }

func (t *entityLinkTool) Name() string { return "entity_link" }
func (t *entityLinkTool) Description() string {
	return "Connect two entities with a typed relation - 'works_on', " +
		"'reports_to', 'belongs_to', 'blocked_by', 'collaborates_with', etc. " +
		"Both entities must already exist (create them with entity_upsert first). " +
		"Reference each by name or id."
}
func (t *entityLinkTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from":     map[string]any{"type": "string", "description": "Source entity - name or id."},
			"to":       map[string]any{"type": "string", "description": "Target entity - name or id."},
			"relation": map[string]any{"type": "string", "description": "The relation type, e.g. 'works_on'."},
			"note":     map[string]any{"type": "string", "description": "Optional context on the relationship."},
		},
		"required": []string{"from", "to", "relation"},
	}
}
func (t *entityLinkTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	if err := t.store.LinkEntities(ctx, wmStr(in, "from"), wmStr(in, "to"), wmStr(in, "relation"), wmStr(in, "note")); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "from": wmStr(in, "from"), "to": wmStr(in, "to"), "relation": wmStr(in, "relation"),
	})
	return string(out), nil
}

// ── entity_get ──────────────────────────────────────────────────────────────

type entityGetTool struct{ store *Store }

func (t *entityGetTool) Name() string { return "entity_get" }
func (t *entityGetTool) Description() string {
	return "Read one entity from the world model - its attributes, summary, and " +
		"every relation it has to other entities. Reference by name or id."
}
func (t *entityGetTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"entity": map[string]any{"type": "string", "description": "Entity name or id."}},
		"required":   []string{"entity"},
	}
}
func (t *entityGetTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	ref := wmStr(in, "entity")
	if ref == "" {
		return "", errors.New("entity_get: entity is required")
	}
	ent, err := t.store.GetEntity(ctx, ref)
	if err != nil {
		return "", err
	}
	if ent == nil {
		return "", fmt.Errorf("entity_get: no entity matching %q", ref)
	}
	b, _ := json.MarshalIndent(ent, "", "  ")
	return string(b), nil
}

// ── entity_search ───────────────────────────────────────────────────────────

type entitySearchTool struct{ store *Store }

func (t *entitySearchTool) Name() string { return "entity_search" }
func (t *entitySearchTool) Description() string {
	return "Search the world model - find entities by free text over name/summary, " +
		"optionally filtered by kind. Ranked by salience. Use this to ground a " +
		"task in what you already know about the boss's world."
}
func (t *entitySearchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Free-text query (optional - omit to list by kind)."},
			"kind":  map[string]any{"type": "string", "description": "Filter to one entity kind (optional)."},
			"limit": map[string]any{"type": "integer", "description": "Max results. Default 25."},
		},
	}
}
func (t *entitySearchTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	limit := 0
	if v, ok := in["limit"].(float64); ok {
		limit = int(v)
	}
	ents, err := t.store.SearchEntities(ctx, wmStr(in, "query"), wmStr(in, "kind"), limit)
	if err != nil {
		return "", err
	}
	out := make([]map[string]any, 0, len(ents))
	for _, e := range ents {
		out = append(out, map[string]any{
			"id": e.ID, "kind": e.Kind, "name": e.Name, "summary": e.Summary, "salience": e.Salience,
		})
	}
	b, _ := json.MarshalIndent(map[string]any{"entities": out}, "", "  ")
	return string(b), nil
}

// ── goal_set ────────────────────────────────────────────────────────────────

type goalSetTool struct{ store *Store }

func (t *goalSetTool) Name() string { return "goal_set" }
func (t *goalSetTool) Description() string {
	return "Create one of your OWN durable goals - an objective you'll pursue " +
		"across sessions, with a living plan. Distinct from the boss's dashboard " +
		"pursuits: these are what YOU are working toward on the boss's behalf " +
		"(\"get the migration shipped\", \"keep the inbox triaged daily\"). Pass " +
		"`plan` as an ordered list of step strings. To update an existing goal, " +
		"pass its `id`. Use goal_update to record progress as you go."
}
func (t *goalSetTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Omit to create; pass to replace an existing goal."},
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"priority":    map[string]any{"type": "string", "enum": []string{"low", "med", "high"}},
			"plan":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ordered plan steps."},
			"due_at":      map[string]any{"type": "string", "description": "Optional ISO-8601 deadline."},
			"entity_id":   map[string]any{"type": "string", "description": "Optional world-model entity this goal is about."},
		},
		"required": []string{"title"},
	}
}
func (t *goalSetTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	g := &Goal{
		ID:          wmStr(in, "id"),
		Title:       wmStr(in, "title"),
		Description: wmStr(in, "description"),
		Priority:    wmStr(in, "priority"),
		EntityID:    wmStr(in, "entity_id"),
		Plan:        parsePlan(in["plan"]),
	}
	if due := wmStr(in, "due_at"); due != "" {
		if ts, err := time.Parse(time.RFC3339, due); err == nil {
			g.DueAt = &ts
		}
	}
	id, err := t.store.UpsertGoal(ctx, g)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "id": id, "title": g.Title, "plan_steps": len(g.Plan),
	})
	return string(out), nil
}

// ── goal_update ─────────────────────────────────────────────────────────────

type goalUpdateTool struct{ store *Store }

func (t *goalUpdateTool) Name() string { return "goal_update" }
func (t *goalUpdateTool) Description() string {
	return "Record progress on one of your goals, re-plan it, mark it blocked, " +
		"or close it. `progress` is APPENDED to the goal's running narrative and " +
		"stamps it as freshly touched (a goal you don't touch gets resurfaced by " +
		"the heartbeat). Pass `plan` as a list of {step, done} to update which " +
		"steps are complete. Set status='blocked' with a `blocker`, or " +
		"status='done' when finished."
}
func (t *goalUpdateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"status":   map[string]any{"type": "string", "enum": []string{"active", "blocked", "done", "abandoned"}},
			"priority": map[string]any{"type": "string", "enum": []string{"low", "med", "high"}},
			"progress": map[string]any{"type": "string", "description": "A progress note - appended to the running narrative."},
			"blocker":  map[string]any{"type": "string", "description": "What's blocking the goal (set with status=blocked)."},
			"plan": map[string]any{
				"type":        "array",
				"description": "Updated plan - list of {step, done} objects.",
				"items":       map[string]any{"type": "object"},
			},
			"due_at": map[string]any{"type": "string", "description": "Optional ISO-8601 deadline."},
		},
		"required": []string{"id"},
	}
}
func (t *goalUpdateTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := wmStr(in, "id")
	if id == "" {
		return "", errors.New("goal_update: id is required")
	}
	var p GoalPatch
	if v := wmStr(in, "status"); v != "" {
		p.Status = &v
	}
	if v := wmStr(in, "priority"); v != "" {
		p.Priority = &v
	}
	if v := wmStr(in, "progress"); v != "" {
		p.ProgressAppend = &v
	}
	if _, ok := in["blocker"]; ok {
		v := wmStr(in, "blocker")
		p.Blocker = &v
	}
	if _, ok := in["plan"]; ok {
		plan := parsePlan(in["plan"])
		p.Plan = &plan
	}
	if due := wmStr(in, "due_at"); due != "" {
		if ts, err := time.Parse(time.RFC3339, due); err == nil {
			p.DueAt = &ts
		}
	}
	if err := t.store.UpdateGoal(ctx, id, p); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "id": id})
	return string(out), nil
}

// ── goal_list ───────────────────────────────────────────────────────────────

type goalListTool struct{ store *Store }

func (t *goalListTool) Name() string { return "goal_list" }
func (t *goalListTool) Description() string {
	return "List your own goals - what you're working toward on the boss's " +
		"behalf, with each goal's status, priority, plan, and progress. Filter " +
		"by status to see just active / blocked / done."
}
func (t *goalListTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"active", "blocked", "done", "abandoned"}, "description": "Optional status filter."},
		},
	}
}
func (t *goalListTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	goals, err := t.store.ListGoals(ctx, wmStr(in, "status"), 0)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{"goals": goals}, "", "  ")
	return string(b), nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func wmStr(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// parsePlan accepts a plan as either a list of step strings or a list of
// {step, done} objects, so goal_set (strings) and goal_update (objects)
// share one parser.
func parsePlan(raw any) []PlanItem {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]PlanItem, 0, len(arr))
	for _, item := range arr {
		switch x := item.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				out = append(out, PlanItem{Step: strings.TrimSpace(x)})
			}
		case map[string]any:
			step, _ := x["step"].(string)
			if strings.TrimSpace(step) == "" {
				continue
			}
			done, _ := x["done"].(bool)
			out = append(out, PlanItem{Step: strings.TrimSpace(step), Done: done})
		}
	}
	return out
}
