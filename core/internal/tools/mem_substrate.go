// mem_substrate — the generic read/write substrate over mem_* tables.
//
// Before this, every new mem_* table required a bespoke Go list tool +
// bespoke Go mutate tool. Now the agent uses:
//
//   mem_list({table, status?, limit?})
//     → returns rows from any mem_* table (information_schema-validated)
//
//   mem_act({table, action, ids[]})
//     → applies a REGISTERED action (mem_action_schemas) to ids
//
//   action_register({table, action, op, column, value?, description?})
//     → registers a new action vocabulary entry
//
// Safety boundaries (load-bearing):
//   - mem_list and mem_act only operate on tables that start with `mem_`
//     and exist in information_schema. Arbitrary table names are
//     rejected up front.
//   - mem_act NEVER takes raw SQL. It looks up the (table, action) row
//     in mem_action_schemas and dispatches to one of FOUR bounded ops:
//     set_status (column=$1), set_timestamp (column=NOW()),
//     set_null (column=NULL), set_bool (column=$1::bool). Anything
//     else fails closed.
//   - column_name lookups for mem_list and mem_act go through
//     information_schema with a strict regexp guard so a malicious
//     hint can't smuggle SQL through the column field.
//   - id arrays are passed as a single parameter (= ANY($1::uuid[])),
//     so SQL injection through ids is impossible.
//
// Net effect: the agent's mutation surface is no longer "what Go tools
// shipped" but "what actions are registered". A new mem_X table only
// requires action_register calls (data writes) to be fully actionable
// from chat. Zero Go edits, zero deploys.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RegisterMemSubstrate(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&memList{pool: pool})
	r.Register(&memAct{pool: pool})
	r.Register(&actionRegister{pool: pool})
	r.Register(&actionList{pool: pool})
}

// Column / table name guard. Postgres identifiers are [A-Za-z_][A-Za-z0-9_]{0,62}
// in our usage; we enforce a stricter ASCII pattern to be defence-in-depth.
var safeIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func validateMemTable(name string) error {
	if !strings.HasPrefix(name, "mem_") {
		return fmt.Errorf("table must start with 'mem_', got %q", name)
	}
	if !safeIdent.MatchString(name) {
		return fmt.Errorf("invalid table name %q", name)
	}
	return nil
}

func validateIdent(name string) error {
	if !safeIdent.MatchString(name) {
		return fmt.Errorf("invalid identifier %q", name)
	}
	return nil
}

// confirmTableExists protects against typo'd table names from the LLM —
// information_schema is the authoritative existence check.
func confirmTableExists(ctx context.Context, pool *pgxpool.Pool, name string) error {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = $1
	`, name).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("table %q does not exist", name)
	}
	return nil
}

// ── mem_list ───────────────────────────────────────────────────────────────

type memList struct{ pool *pgxpool.Pool }

func (t *memList) Name() string        { return "mem_list" }
func (t *memList) ReadOnly() bool      { return true }
func (t *memList) Description() string {
	return "Generic reader for any mem_* table. Returns id + every TEXT/UUID/" +
		"TIMESTAMP column up to 12 columns. Pass `status` (or `status_eq`) to " +
		"filter on the status column when present, `unread_only` for tables " +
		"with that column, `limit` (default 100, max 500). Use system_map " +
		"FIRST to discover which tables exist and which columns matter."
}
func (t *memList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"table":       map[string]any{"type": "string", "description": "mem_* table name from system_map."},
			"status":      map[string]any{"type": "string", "description": "Optional status filter (e.g. 'open', 'pending')."},
			"unread_only": map[string]any{"type": "boolean", "description": "When true and table has an `unread` column, filter unread=true."},
			"limit":       map[string]any{"type": "integer", "default": 100},
		},
		"required": []string{"table"},
	}
}
func (t *memList) Execute(ctx context.Context, in map[string]any) (string, error) {
	table := strString(in, "table")
	if err := validateMemTable(table); err != nil {
		return "", err
	}
	if err := confirmTableExists(ctx, t.pool, table); err != nil {
		return "", err
	}
	limit := 100
	if v, ok := numFloat(in["limit"]); ok && v > 0 {
		limit = int(v)
	}
	if limit > 500 {
		limit = 500
	}
	statusFilter := strString(in, "status")
	unreadOnly, _ := in["unread_only"].(bool)

	// Discover columns for this table — text-castable subset.
	cols, hasStatus, hasUnread, err := t.readColumns(ctx, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("table %q has no readable columns", table)
	}

	// Build SELECT col1::text, col2::text, ... — uniform string output so
	// the agent gets stable JSON regardless of underlying types.
	selects := make([]string, 0, len(cols))
	for _, c := range cols {
		selects = append(selects, "COALESCE("+quoteIdent(c)+"::text, '')")
	}
	q := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdent(table)
	args := []any{}
	wheres := []string{}
	if statusFilter != "" && hasStatus {
		wheres = append(wheres, "status = $"+fmt.Sprintf("%d", len(args)+1))
		args = append(args, statusFilter)
	}
	if unreadOnly && hasUnread {
		wheres = append(wheres, "unread = true")
	}
	if len(wheres) > 0 {
		q += " WHERE " + strings.Join(wheres, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY 1 DESC LIMIT %d", limit)
	return queryRowsAsJSON(ctx, t.pool, q, args, cols)
}

// readColumns pulls a short, predictable column list from information_schema
// for a given mem_* table. Caps at 12 to keep payloads bounded — if the
// agent needs more it can name them explicitly via a future tool. Returns
// the chosen columns, whether 'status' and 'unread' are among them.
func (t *memList) readColumns(ctx context.Context, table string) ([]string, bool, bool, error) {
	rows, err := t.pool.Query(ctx, `
		SELECT column_name, data_type
		  FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1
		 ORDER BY ordinal_position
	`, table)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()

	preferred := []string{"id", "title", "subtitle", "question", "name", "kind", "source", "status", "from_name", "subject", "preview", "url"}
	preferSet := map[string]bool{}
	for _, p := range preferred {
		preferSet[p] = true
	}

	var primary, fallback []string
	var hasStatus, hasUnread bool
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, false, false, err
		}
		if name == "status" {
			hasStatus = true
		}
		if name == "unread" {
			hasUnread = true
		}
		if preferSet[name] {
			primary = append(primary, name)
		} else {
			fallback = append(fallback, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
	}
	out := primary
	// Top up with fallback columns to reach 12 max.
	for _, c := range fallback {
		if len(out) >= 12 {
			break
		}
		out = append(out, c)
	}
	return out, hasStatus, hasUnread, nil
}

// ── mem_act ────────────────────────────────────────────────────────────────

type memAct struct{ pool *pgxpool.Pool }

func (t *memAct) Name() string { return "mem_act" }
func (t *memAct) Description() string {
	return "Apply a REGISTERED action (mem_action_schemas) to one or more rows " +
		"in a mem_* table by id. The action vocabulary is bounded: set_status, " +
		"set_timestamp, set_null, set_bool — no arbitrary SQL. Use action_list " +
		"or system_map to see which (table, action) pairs are registered. To " +
		"register a new action, use action_register."
}
func (t *memAct) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"table":  map[string]any{"type": "string"},
			"action": map[string]any{"type": "string", "description": "Registered action name (e.g. 'dismissed', 'done', 'pause')."},
			"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "One or more row ids (UUIDs)."},
		},
		"required": []string{"table", "action", "ids"},
	}
}
func (t *memAct) Execute(ctx context.Context, in map[string]any) (string, error) {
	table := strString(in, "table")
	if err := validateMemTable(table); err != nil {
		return "", err
	}
	if err := confirmTableExists(ctx, t.pool, table); err != nil {
		return "", err
	}
	action := strString(in, "action")
	if action == "" {
		return "", errors.New("action required")
	}
	rawIDs, _ := in["ids"].([]any)
	ids := make([]string, 0, len(rawIDs))
	for _, v := range rawIDs {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			ids = append(ids, strings.TrimSpace(s))
		}
	}
	if len(ids) == 0 {
		return "", errors.New("ids must contain at least one id")
	}

	// Look up the bounded action schema.
	var (
		op, column string
		value      *string
	)
	err := t.pool.QueryRow(ctx, `
		SELECT op, column_name, value
		  FROM mem_action_schemas
		 WHERE table_name = $1 AND action_name = $2
	`, table, action).Scan(&op, &column, &value)
	if err != nil {
		return "", fmt.Errorf("no action %q registered for table %q (call action_register first or check action_list)", action, table)
	}
	if err := validateIdent(column); err != nil {
		return "", fmt.Errorf("invalid registered column %q: %w", column, err)
	}

	// Dispatch on bounded op. Updated-rows count is returned for the
	// agent to verify.
	var ct int64
	switch op {
	case "set_status":
		if value == nil {
			return "", fmt.Errorf("set_status requires value in schema")
		}
		tag, e := t.pool.Exec(ctx,
			"UPDATE "+quoteIdent(table)+" SET "+quoteIdent(column)+" = $1, updated_at = COALESCE(updated_at, NOW()) WHERE id::text = ANY($2)",
			*value, ids,
		)
		if e != nil {
			return "", e
		}
		ct = tag.RowsAffected()
	case "set_timestamp":
		tag, e := t.pool.Exec(ctx,
			"UPDATE "+quoteIdent(table)+" SET "+quoteIdent(column)+" = NOW() WHERE id::text = ANY($1)",
			ids,
		)
		if e != nil {
			return "", e
		}
		ct = tag.RowsAffected()
	case "set_null":
		tag, e := t.pool.Exec(ctx,
			"UPDATE "+quoteIdent(table)+" SET "+quoteIdent(column)+" = NULL WHERE id::text = ANY($1)",
			ids,
		)
		if e != nil {
			return "", e
		}
		ct = tag.RowsAffected()
	case "set_bool":
		if value == nil {
			return "", fmt.Errorf("set_bool requires value in schema")
		}
		b := strings.EqualFold(strings.TrimSpace(*value), "true")
		tag, e := t.pool.Exec(ctx,
			"UPDATE "+quoteIdent(table)+" SET "+quoteIdent(column)+" = $1 WHERE id::text = ANY($2)",
			b, ids,
		)
		if e != nil {
			return "", e
		}
		ct = tag.RowsAffected()
	default:
		return "", fmt.Errorf("unknown op %q in schema (must be set_status|set_timestamp|set_null|set_bool)", op)
	}

	out, _ := json.Marshal(map[string]any{
		"ok":      true,
		"table":   table,
		"action":  action,
		"updated": ct,
		"id_count": len(ids),
	})
	return string(out), nil
}

// ── action_register ────────────────────────────────────────────────────────

type actionRegister struct{ pool *pgxpool.Pool }

func (t *actionRegister) Name() string { return "action_register" }
func (t *actionRegister) Description() string {
	return "Register a new (table, action) entry in mem_action_schemas so " +
		"mem_act can apply it. Bounded vocabulary: op must be one of " +
		"set_status, set_timestamp, set_null, set_bool. column_name must be " +
		"an existing column on the table (validated against information_schema)." +
		" Idempotent: re-registering updates the existing row."
}
func (t *actionRegister) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"table":       map[string]any{"type": "string"},
			"action":      map[string]any{"type": "string"},
			"op":          map[string]any{"type": "string", "enum": []string{"set_status", "set_timestamp", "set_null", "set_bool"}},
			"column":      map[string]any{"type": "string"},
			"value":       map[string]any{"type": "string", "description": "Required for set_status (status literal) and set_bool ('true'/'false'). Omit for set_timestamp / set_null."},
			"description": map[string]any{"type": "string"},
		},
		"required": []string{"table", "action", "op", "column"},
	}
}
func (t *actionRegister) Execute(ctx context.Context, in map[string]any) (string, error) {
	table := strString(in, "table")
	if err := validateMemTable(table); err != nil {
		return "", err
	}
	if err := confirmTableExists(ctx, t.pool, table); err != nil {
		return "", err
	}
	action := strString(in, "action")
	op := strString(in, "op")
	column := strString(in, "column")
	if action == "" || op == "" || column == "" {
		return "", errors.New("action, op, column required")
	}
	switch op {
	case "set_status", "set_timestamp", "set_null", "set_bool":
	default:
		return "", fmt.Errorf("op must be set_status|set_timestamp|set_null|set_bool")
	}
	if err := validateIdent(column); err != nil {
		return "", err
	}
	// Confirm the column exists on the table — defence against typos.
	var n int
	if err := t.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1 AND column_name=$2
	`, table, column).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("column %q does not exist on %q", column, table)
	}

	value := strString(in, "value")
	desc := strString(in, "description")

	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_action_schemas (table_name, action_name, op, column_name, value, description, source)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), $6, 'agent')
		ON CONFLICT (table_name, action_name) DO UPDATE
		   SET op           = EXCLUDED.op,
		       column_name  = EXCLUDED.column_name,
		       value        = EXCLUDED.value,
		       description  = COALESCE(NULLIF(EXCLUDED.description,''), mem_action_schemas.description),
		       source       = 'agent',
		       updated_at   = NOW()
	`, table, action, op, column, value, desc)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":     true,
		"table":  table,
		"action": action,
		"op":     op,
		"column": column,
	})
	return string(out), nil
}

// ── action_list ────────────────────────────────────────────────────────────

type actionList struct{ pool *pgxpool.Pool }

func (t *actionList) Name() string        { return "action_list" }
func (t *actionList) ReadOnly() bool      { return true }
func (t *actionList) Description() string {
	return "List registered action schemas. Optionally filter by table. Returns " +
		"every (table, action, op, column, value, source) tuple — the agent's " +
		"complete mutation vocabulary."
}
func (t *actionList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"table": map[string]any{"type": "string", "description": "Optional table name filter."},
		},
	}
}
func (t *actionList) Execute(ctx context.Context, in map[string]any) (string, error) {
	table := strString(in, "table")
	q := `SELECT table_name, action_name, op, column_name,
	             COALESCE(value,''), COALESCE(description,''),
	             COALESCE(source,'')
	        FROM mem_action_schemas`
	args := []any{}
	if table != "" {
		if err := validateMemTable(table); err != nil {
			return "", err
		}
		q += ` WHERE table_name = $1`
		args = append(args, table)
	}
	q += ` ORDER BY table_name, action_name`
	return queryRowsAsJSON(ctx, t.pool, q, args,
		[]string{"table", "action", "op", "column", "value", "description", "source"})
}
