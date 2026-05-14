package initiative

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires the four agent-facing initiative + economics tools:
//
//	notify              — reach the boss, urgency-routed
//	notification_digest — flush batched low-urgency notifications
//	cost_record         — log a cost-incurring event
//	budget_status       — read the cost rollup vs the budget
//
// Lives in package initiative (which imports tools) — no import cycle.
func RegisterTools(reg *tools.Registry, notifier *Notifier, store *Store) {
	if reg == nil || notifier == nil || store == nil {
		return
	}
	reg.Register(&notifyTool{notifier: notifier})
	reg.Register(&notificationDigestTool{notifier: notifier})
	reg.Register(&costRecordTool{store: store})
	reg.Register(&budgetStatusTool{store: store})
}

// ── notify ──────────────────────────────────────────────────────────────────

type notifyTool struct{ notifier *Notifier }

func (t *notifyTool) Name() string { return "notify" }
func (t *notifyTool) Description() string {
	return "Reach the boss. You pick the urgency; the policy routes it:\n" +
		"  • urgent → pushed to the phone immediately (use sparingly — only " +
		"things worth an interruption: a deadline, a failure, something time-" +
		"sensitive).\n" +
		"  • normal → a dashboard card the boss sees next time they look.\n" +
		"  • low → batched into the next digest, so small updates don't " +
		"interrupt.\n" +
		"Every notification is logged. Default urgency is normal — reserve " +
		"urgent for things that genuinely can't wait."
}
func (t *notifyTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":   map[string]any{"type": "string", "description": "The headline — what the boss needs to know."},
			"body":    map[string]any{"type": "string", "description": "Optional detail."},
			"urgency": map[string]any{"type": "string", "enum": []string{"urgent", "normal", "low"}, "description": "urgent = push now · normal = dashboard card · low = batched digest. Default normal."},
			"url":     map[string]any{"type": "string", "description": "Optional deep link to the relevant Studio surface."},
			"source":  map[string]any{"type": "string", "description": "What's reaching out — a skill name, workflow name, cron name. Default 'agent'."},
		},
		"required": []string{"title"},
	}
}
func (t *notifyTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	n := &Notification{
		Title:   initStr(in, "title"),
		Body:    initStr(in, "body"),
		URL:     initStr(in, "url"),
		Source:  initStr(in, "source"),
		Urgency: Urgency(initStr(in, "urgency")),
	}
	if err := t.notifier.Send(ctx, n); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "id": n.ID, "urgency": string(n.Urgency), "channel": n.Channel, "status": n.Status,
	})
	return string(out), nil
}

// ── notification_digest ─────────────────────────────────────────────────────

type notificationDigestTool struct{ notifier *Notifier }

func (t *notificationDigestTool) Name() string { return "notification_digest" }
func (t *notificationDigestTool) Description() string {
	return "Flush every batched (low-urgency) notification into a single digest " +
		"push to the boss. Call this on a cadence — e.g. from a morning cron — " +
		"so small updates accumulate and arrive once instead of pinging all day."
}
func (t *notificationDigestTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *notificationDigestTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	n, err := t.notifier.Digest(ctx)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Flushed %d batched notification(s) into a digest.", n)
	if n == 0 {
		msg = "Nothing batched — no digest sent."
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "flushed": n, "message": msg})
	return string(out), nil
}

// ── cost_record ─────────────────────────────────────────────────────────────

type costRecordTool struct{ store *Store }

func (t *costRecordTool) Name() string { return "cost_record" }
func (t *costRecordTool) Description() string {
	return "Log a cost-incurring event to the cost ledger — an expensive API " +
		"call, a long LLM run, a paid tool. This is how you stay aware of what " +
		"you're spending so budget_status means something. Record the estimated " +
		"USD cost and, when you have them, the units + quantity (e.g. tokens)."
}
func (t *costRecordTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{"type": "string", "description": "llm | api | tool | workflow | … (free-form)."},
			"subject":  map[string]any{"type": "string", "description": "What incurred it — model name, tool name, API name."},
			"cost_usd": map[string]any{"type": "number", "description": "Estimated cost in USD."},
			"units":    map[string]any{"type": "string", "description": "Unit of the quantity, e.g. 'tokens', 'calls'."},
			"quantity": map[string]any{"type": "number", "description": "How many units."},
			"note":     map[string]any{"type": "string", "description": "Optional context."},
		},
		"required": []string{"category", "cost_usd"},
	}
}
func (t *costRecordTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	e := &CostEvent{
		Category: initStr(in, "category"),
		Subject:  initStr(in, "subject"),
		Units:    initStr(in, "units"),
		Note:     initStr(in, "note"),
	}
	if v, ok := in["cost_usd"].(float64); ok {
		e.CostUSD = v
	}
	if v, ok := in["quantity"].(float64); ok {
		e.Quantity = v
	}
	if err := t.store.RecordCost(ctx, e); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "id": e.ID, "category": e.Category, "cost_usd": e.CostUSD})
	return string(out), nil
}

// ── budget_status ───────────────────────────────────────────────────────────

type budgetStatusTool struct{ store *Store }

func (t *budgetStatusTool) Name() string { return "budget_status" }
func (t *budgetStatusTool) Description() string {
	return "Read the cost rollup — total spend over a window, broken down by " +
		"category, compared against the configured budget (INFINITY_BUDGET_USD). " +
		"Check this before committing to an expensive operation: if you're over " +
		"or near budget, throttle or defer costly work and tell the boss."
}
func (t *budgetStatusTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"window_days": map[string]any{"type": "integer", "description": "Rollup window in days. Default 30."},
		},
	}
}
func (t *budgetStatusTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	window := 0
	if v, ok := in["window_days"].(float64); ok {
		window = int(v)
	}
	b, err := t.store.BudgetRollup(ctx, window)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(b, "", "  ")
	return string(out), nil
}

func initStr(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
