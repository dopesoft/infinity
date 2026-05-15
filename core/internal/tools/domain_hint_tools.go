// domain_hint_add / domain_hint_list — the agent extends its own UI/
// data/tool topology at runtime.
//
// Why this exists: system_map auto-discovers tables (information_schema)
// and tools (registry) but pairing them by name convention only works
// for ~80% of cases. The other 20% — multi-word tables, irregular
// plurals, semantic mismatches (mem_curiosity_questions → question_*) —
// used to require a Go edit + rebuild. Now the agent can register the
// mapping itself the FIRST time it learns the convention, and every
// future system_map() reflects it without a deploy.
//
// This is the autonomous self-extension move for the introspection
// layer. The agent's understanding of its own UI is now data, not code.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterDomainHintTools wires domain_hint_add + domain_hint_list.
// Pinned by the agent loop's default active set so introspection is
// always one-shot reachable. No-op when pool is nil.
func RegisterDomainHintTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&domainHintAdd{pool: pool})
	r.Register(&domainHintList{pool: pool})
}

// ── domain_hint_add ───────────────────────────────────────────────────────

type domainHintAdd struct{ pool *pgxpool.Pool }

func (t *domainHintAdd) Name() string { return "domain_hint_add" }
func (t *domainHintAdd) Description() string {
	return "Persist a mapping between a mem_* table and the tool prefix that " +
		"acts on it. Use this the first time you discover that a table's " +
		"agent tools live under a non-convention prefix (e.g. " +
		"mem_curiosity_questions → question_*). The next system_map() reflects " +
		"the hint automatically; no deploy needed. Idempotent — re-asserting " +
		"the same table updates the existing row instead of erroring."
}
func (t *domainHintAdd) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"table_name":  map[string]any{"type": "string", "description": "The mem_* table this hint covers."},
			"tool_prefix": map[string]any{"type": "string", "description": "Tool name prefix (e.g. 'question', 'surface'). Matches `<prefix>_*` and exact `<prefix>`."},
			"display_as":  map[string]any{"type": "string", "description": "Human-readable name for system_map output."},
			"notes":       map[string]any{"type": "string", "description": "Optional rationale for the mapping."},
		},
		"required": []string{"table_name", "tool_prefix"},
	}
}
func (t *domainHintAdd) Execute(ctx context.Context, in map[string]any) (string, error) {
	table := strString(in, "table_name")
	prefix := strString(in, "tool_prefix")
	if table == "" || prefix == "" {
		return "", errors.New("table_name and tool_prefix required")
	}
	if !strings.HasPrefix(table, "mem_") {
		return "", fmt.Errorf("table_name must start with 'mem_', got %q", table)
	}
	display := strString(in, "display_as")
	notes := strString(in, "notes")

	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_domain_hints (table_name, tool_prefix, display_as, notes, source)
		VALUES ($1, $2, $3, $4, 'agent')
		ON CONFLICT (table_name) DO UPDATE
		   SET tool_prefix = EXCLUDED.tool_prefix,
		       display_as  = COALESCE(NULLIF(EXCLUDED.display_as,''), mem_domain_hints.display_as),
		       notes       = COALESCE(NULLIF(EXCLUDED.notes,''), mem_domain_hints.notes),
		       source      = 'agent',
		       updated_at  = NOW()
	`, table, prefix, display, notes)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":          true,
		"table":       table,
		"tool_prefix": prefix,
		"note":        "Next system_map() will reflect this mapping.",
	})
	return string(out), nil
}

// ── domain_hint_list ──────────────────────────────────────────────────────

type domainHintList struct{ pool *pgxpool.Pool }

func (t *domainHintList) Name() string        { return "domain_hint_list" }
func (t *domainHintList) ReadOnly() bool      { return true }
func (t *domainHintList) Description() string {
	return "List all persisted table↔tool-prefix mappings used by system_map. " +
		"Useful for auditing what the agent has learned vs what was seeded."
}
func (t *domainHintList) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *domainHintList) Execute(ctx context.Context, _ map[string]any) (string, error) {
	rows, err := t.pool.Query(ctx, `
		SELECT table_name, tool_prefix, COALESCE(display_as,''),
		       COALESCE(notes,''), COALESCE(source,''),
		       to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM mem_domain_hints
		 ORDER BY source DESC, table_name
	`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type row struct {
		Table     string `json:"table_name"`
		Prefix    string `json:"tool_prefix"`
		DisplayAs string `json:"display_as,omitempty"`
		Notes     string `json:"notes,omitempty"`
		Source    string `json:"source"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Table, &r.Prefix, &r.DisplayAs, &r.Notes, &r.Source, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return "", err
		}
		out = append(out, r)
	}
	b, _ := json.Marshal(map[string]any{"count": len(out), "hints": out})
	return string(b), nil
}
