// system_map — runtime introspection of the agent's own UI/data/tool
// topology. Called by the agent to answer "for any user-facing surface,
// which table backs it and which tool acts on it?" — with zero
// prompt-level memorisation.
//
// Hardened version. What's runtime-derived:
//
//   1. Tables — queried from information_schema at every call.
//   2. Tools — walked from the live Registry at every call.
//   3. Domain hints — read from mem_domain_hints (persistent DB table,
//      seeded by migration 028, extendable by the agent via
//      domain_hint_add). No Go slice, no rebuild required.
//   4. Read/mutate classification — uses the optional ReadOnlyTool
//      interface (registry.go) when implemented, falls back to suffix
//      heuristic otherwise.
//   5. Open counts — heuristic ladder over common status columns,
//      degrades to total row count.
//   6. Gaps — first-class output: tables without tools, missing list/
//      mutate halves. The agent sees what's broken and can propose
//      tools (skill_propose) to fill them.
//
// The ONLY remaining hardcoding is the heuristic ladder for counts (six
// status column patterns) and the singular-strip convention (trailing
// `s` for plural→singular). Both apply uniformly across all tables —
// no per-domain branching.

package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterSystemMap wires system_map. Needs both the live registry and
// the DB pool — without both, introspection degrades to whichever half
// is available.
func RegisterSystemMap(r *Registry, pool *pgxpool.Pool) {
	if r == nil {
		return
	}
	r.Register(&systemMap{registry: r, pool: pool})
}

type systemMap struct {
	registry *Registry
	pool     *pgxpool.Pool
}

func (t *systemMap) Name() string { return "system_map" }
func (t *systemMap) Description() string {
	return "Introspect the agent's own UI/data/tool topology — runtime, from " +
		"the live DB schema, tool registry, and persistent domain hints. " +
		"Returns: per-domain surfaces (table + list_tools + mutate_tools + " +
		"live open_count + recipe), GAPS where a table has no tool or only " +
		"half the verb pair, and CAPABILITIES (non-domain tools). Call FIRST " +
		"for any 'do X on my dashboard / list / queue' task. To extend the " +
		"map for a new irregular table, call domain_hint_add — the next " +
		"system_map() reflects it without a deploy."
}
func (t *systemMap) ReadOnly() bool { return true }
func (t *systemMap) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"include_capabilities": map[string]any{
				"type": "boolean", "default": true,
				"description": "Include non-domain tools (delegate, web_search, MCP) in the response.",
			},
			"include_gaps": map[string]any{
				"type": "boolean", "default": true,
				"description": "Include tables-without-tools and asymmetric domains.",
			},
		},
	}
}

type surfaceEntry struct {
	Domain      string   `json:"domain"`
	DisplayAs   string   `json:"display_as,omitempty"`
	Table       string   `json:"table"`
	ListTools   []string `json:"list_tools"`
	MutateTools []string `json:"mutate_tools"`
	Actions     []string `json:"actions,omitempty"` // registered mem_act vocabulary for this table
	OpenCount   int      `json:"open_count"`
	CountNote   string   `json:"count_note,omitempty"`
	Recipe      string   `json:"recipe,omitempty"`
	HintSource  string   `json:"hint_source,omitempty"`
}

type gapEntry struct {
	Kind string `json:"kind"`
	Item string `json:"item"`
	Hint string `json:"hint"`
}

func (t *systemMap) Execute(ctx context.Context, in map[string]any) (string, error) {
	includeCaps := boolDefault(in, "include_capabilities", true)
	includeGaps := boolDefault(in, "include_gaps", true)

	tables, tableErr := t.discoverTables(ctx)
	hints, hintsErr := t.loadHints(ctx)
	allTools := t.registry.All()
	actions := t.loadActions(ctx)
	usedTools := map[string]bool{}

	surfaces := make([]surfaceEntry, 0, len(tables))
	for _, table := range tables {
		prefix, display, src := domainFor(table, hints)
		hint := hints[table]
		e := surfaceEntry{
			Domain:     prefix,
			DisplayAs:  display,
			Table:      table,
			HintSource: src,
			Actions:    actions[table],
		}
		for name, tool := range allTools {
			if !matchesDomain(name, prefix) {
				continue
			}
			usedTools[name] = true
			if IsReadOnly(tool) {
				e.ListTools = append(e.ListTools, name)
			} else {
				e.MutateTools = append(e.MutateTools, name)
			}
		}
		sort.Strings(e.ListTools)
		sort.Strings(e.MutateTools)
		e.OpenCount, e.CountNote = t.countWith(ctx, table, hint.CountFilter)
		// Recipe priority: domain-specific tools if present, else the
		// generic mem_act + mem_list pair if actions are registered.
		switch {
		case len(e.ListTools) > 0 && len(e.MutateTools) > 0:
			e.Recipe = e.ListTools[0] + " → " + e.MutateTools[0] + "({id, ...})"
		case len(e.Actions) > 0:
			e.Recipe = `mem_list({table:"` + table + `"}) → mem_act({table, action, ids})`
		}
		surfaces = append(surfaces, e)
	}
	sort.Slice(surfaces, func(i, j int) bool { return surfaces[i].Domain < surfaces[j].Domain })

	capabilities := []map[string]any{}
	if includeCaps {
		buckets := map[string][]string{}
		for n := range allTools {
			if usedTools[n] {
				continue
			}
			buckets[bucketOf(n)] = append(buckets[bucketOf(n)], n)
		}
		keys := make([]string, 0, len(buckets))
		for k := range buckets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sort.Strings(buckets[k])
			capabilities = append(capabilities, map[string]any{
				"bucket": k,
				"tools":  buckets[k],
				"count":  len(buckets[k]),
			})
		}
	}

	gaps := []gapEntry{}
	if includeGaps {
		for _, s := range surfaces {
			if len(s.ListTools) == 0 && len(s.MutateTools) == 0 {
				gaps = append(gaps, gapEntry{
					Kind: "table_without_tool",
					Item: s.Table,
					Hint: "no agent tool maps to this table; propose " + s.Domain + "_list and a mutate verb",
				})
				continue
			}
			if len(s.ListTools) == 0 {
				gaps = append(gaps, gapEntry{
					Kind: "missing_list",
					Item: s.Table,
					Hint: "has mutate tool(s) but no read tool; propose " + s.Domain + "_list",
				})
			}
			if len(s.MutateTools) == 0 {
				gaps = append(gaps, gapEntry{
					Kind: "missing_mutate",
					Item: s.Table,
					Hint: "read-only; propose a mutate verb for " + s.Domain,
				})
			}
		}
	}

	resp := map[string]any{
		"version":   3,
		"principle": "Pair list_tools → mutate_tools to act. Never ask the boss for ids. To map a new irregular table, call domain_hint_add — no deploy needed.",
		"surfaces":  surfaces,
		"gap_count": len(gaps),
	}
	if includeCaps {
		resp["capabilities"] = capabilities
	}
	if includeGaps {
		resp["gaps"] = gaps
	}
	if tableErr != nil {
		resp["table_discovery_error"] = tableErr.Error()
	}
	if hintsErr != nil {
		resp["hints_load_error"] = hintsErr.Error()
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

// loadHints reads mem_domain_hints from the live DB. Returns a
// map[table]→hint. Failure is non-fatal — system_map falls back to the
// pure-convention path (which still handles ~80% of tables correctly).
type loadedHint struct {
	Prefix      string
	DisplayAs   string
	Source      string
	CountFilter string // "open" | "pending" | "proposed" | "active" | "enabled" | "unread" | "total" | <status literal>
}

func (t *systemMap) loadHints(ctx context.Context) (map[string]loadedHint, error) {
	if t.pool == nil {
		return map[string]loadedHint{}, nil
	}
	// Try with count_filter first (post-migration 029). Fall back to the
	// older schema if the column doesn't exist yet — keeps the tool
	// resilient against partial-migrate states.
	rows, err := t.pool.Query(ctx, `
		SELECT table_name, tool_prefix, COALESCE(display_as,''),
		       COALESCE(source,''), COALESCE(count_filter,'')
		  FROM mem_domain_hints
	`)
	if err != nil {
		// Retry without count_filter for pre-029 deployments.
		rows2, err2 := t.pool.Query(ctx, `
			SELECT table_name, tool_prefix, COALESCE(display_as,''), COALESCE(source,'')
			  FROM mem_domain_hints
		`)
		if err2 != nil {
			return map[string]loadedHint{}, err
		}
		defer rows2.Close()
		out := map[string]loadedHint{}
		for rows2.Next() {
			var table, prefix, display, source string
			if err := rows2.Scan(&table, &prefix, &display, &source); err != nil {
				return out, err
			}
			out[table] = loadedHint{Prefix: prefix, DisplayAs: display, Source: source}
		}
		return out, rows2.Err()
	}
	defer rows.Close()
	out := map[string]loadedHint{}
	for rows.Next() {
		var table, prefix, display, source, cfilter string
		if err := rows.Scan(&table, &prefix, &display, &source, &cfilter); err != nil {
			return out, err
		}
		out[table] = loadedHint{Prefix: prefix, DisplayAs: display, Source: source, CountFilter: cfilter}
	}
	return out, rows.Err()
}

func (t *systemMap) discoverTables(ctx context.Context) ([]string, error) {
	if t.pool == nil {
		return nil, nil
	}
	rows, err := t.pool.Query(ctx, `
		SELECT table_name
		  FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_name LIKE 'mem\_%' ESCAPE '\'
		 ORDER BY table_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// loadActions pulls the registered action vocabulary keyed by table.
// Best-effort: a missing mem_action_schemas (pre-029) returns empty
// rather than erroring.
func (t *systemMap) loadActions(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	if t.pool == nil {
		return out
	}
	rows, err := t.pool.Query(ctx, `
		SELECT table_name, action_name
		  FROM mem_action_schemas
		 ORDER BY table_name, action_name
	`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var table, action string
		if err := rows.Scan(&table, &action); err != nil {
			continue
		}
		out[table] = append(out[table], action)
	}
	return out
}

// countWith uses an explicit hint when provided, falling back to the
// heuristic ladder. Symbolic hints are interpreted:
//   "open" | "pending" | "proposed" | "active"  → WHERE status = <hint>
//   "enabled"                                    → WHERE enabled = true
//   "unread"                                     → WHERE read_at IS NULL (or unread=true)
//   "total"                                      → no filter
//   anything else                                → treat as status literal
func (t *systemMap) countWith(ctx context.Context, table, hint string) (int, string) {
	if t.pool == nil {
		return 0, "no pool"
	}
	if hint != "" {
		var (
			sql, note string
		)
		switch hint {
		case "enabled":
			sql = `SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE enabled = true`
			note = "enabled=true (hint)"
		case "unread":
			// Try unread=true first, fall back to read_at IS NULL.
			var n int
			if err := t.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)+` WHERE unread = true`).Scan(&n); err == nil {
				return n, "unread=true (hint)"
			}
			sql = `SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE read_at IS NULL`
			note = "read_at IS NULL (hint)"
		case "total":
			sql = `SELECT COUNT(*) FROM ` + quoteIdent(table)
			note = "total rows (hint)"
		default:
			sql = `SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE status = $1`
			var n int
			if err := t.pool.QueryRow(ctx, sql, hint).Scan(&n); err == nil {
				return n, "status='" + hint + "' (hint)"
			}
			// fall through to heuristic if the hinted column scheme doesn't apply
		}
		if sql != "" && !strings.Contains(sql, "$1") {
			var n int
			if err := t.pool.QueryRow(ctx, sql).Scan(&n); err == nil {
				return n, note
			}
		}
	}
	return t.countOpen(ctx, table)
}

func (t *systemMap) countOpen(ctx context.Context, table string) (int, string) {
	if t.pool == nil {
		return 0, "no pool"
	}
	candidates := []struct {
		sql, note string
	}{
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE status='open'`, "status='open'"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE status='pending'`, "status='pending'"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE status='proposed'`, "status='proposed'"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE status='active'`, "status='active'"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE enabled=true`, "enabled=true"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table) + ` WHERE read_at IS NULL`, "unread"},
		{`SELECT COUNT(*) FROM ` + quoteIdent(table), "total rows"},
	}
	for _, c := range candidates {
		var n int
		if err := t.pool.QueryRow(ctx, c.sql).Scan(&n); err == nil {
			return n, c.note
		}
	}
	return 0, "no row-count strategy succeeded"
}

// domainFor returns (prefix, displayName, hintSource). Hint takes
// precedence; otherwise the convention (strip `mem_`, drop trailing
// `s`) applies. The third return is "" for convention, otherwise the
// `source` column from mem_domain_hints (so the agent can see whether
// a hint is seeded vs agent-learned).
func domainFor(table string, hints map[string]loadedHint) (string, string, string) {
	if h, ok := hints[table]; ok && h.Prefix != "" {
		display := h.DisplayAs
		if display == "" {
			display = strings.ReplaceAll(strings.TrimPrefix(table, "mem_"), "_", " ")
		}
		return h.Prefix, display, h.Source
	}
	stripped := strings.TrimPrefix(table, "mem_")
	display := strings.ReplaceAll(stripped, "_", " ")
	if strings.HasSuffix(stripped, "s") && len(stripped) > 3 {
		stripped = strings.TrimSuffix(stripped, "s")
	}
	return stripped, display, "convention"
}

func matchesDomain(toolName, prefix string) bool {
	if prefix == "" {
		return false
	}
	if strings.Contains(toolName, "__") {
		return false
	}
	if toolName == prefix {
		return true
	}
	return strings.HasPrefix(toolName, prefix+"_")
}

func bucketOf(name string) string {
	if i := strings.Index(name, "__"); i > 0 {
		return name[:i]
	}
	if i := strings.Index(name, "_"); i > 0 {
		return name[:i]
	}
	return name
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func boolDefault(in map[string]any, k string, def bool) bool {
	if v, ok := in[k].(bool); ok {
		return v
	}
	return def
}
