package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MIGRATION DRIFT — "are the migrations this binary carries actually applied?"
//
// Nothing in the system could answer that question until now, and the cost of
// not being able to has been paid repeatedly. Migrations 011 through 014 sat
// unapplied in production for WEEKS while dashboard handlers logged `relation
// "mem_tasks" does not exist` and the AGI loops wrote into tables that were not
// there — because `infinity serve` does not auto-migrate, merging a migration
// does nothing to prod on its own, and a prior session asserted the schema was
// current without checking (CLAUDE.md, "Migrations").
//
// So the only honest answer to "are migrations applied?" was to run the
// migrator and read its output. This makes the same answer available in
// process: /readyz reports it so the off-box watchdog can see drift, and the
// plan-step done-gate uses it so "migrations applied" can be PROVEN rather
// than ticked off on the model's word.
//
// Deliberately read-only. It reports drift; it never applies anything. Applying
// stays an explicit `infinity migrate`, because a background process quietly
// mutating the boss's schema is a different and much worse failure.

// Querier is the read surface both *pgx.Conn and *pgxpool.Pool satisfy, so
// this works from the migrate command and from the running server alike.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Names returns the embedded migration filenames in apply order.
func Names() ([]string, error) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// Pending returns the embedded migrations this binary carries that the live
// database has not recorded, in apply order.
//
// An error is returned rather than an empty slice whenever the answer cannot
// be established — no database, an unreadable schema_migrations table. Empty
// means "verified: nothing pending", never "I could not look"; conflating the
// two here would put a false green on the exact question that has already cost
// weeks of silent breakage.
func Pending(ctx context.Context, q Querier) ([]string, error) {
	if q == nil {
		return nil, fmt.Errorf("no database connection, so migration state is unknown")
	}
	names, err := Names()
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the binary carries no migrations, which cannot be right")
	}

	rows, err := q.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var pending []string
	for _, n := range names {
		if _, ok := applied[n]; !ok {
			pending = append(pending, n)
		}
	}
	return pending, nil
}

// SchemaVersion is the newest migration this binary carries — the version the
// database would be at once everything is applied. Empty when the set is
// unreadable.
func SchemaVersion() string {
	names, err := Names()
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[len(names)-1]
}
