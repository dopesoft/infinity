package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/db"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

func migrateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply SQL migrations against $DATABASE_URL (uses embedded migrations by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}

			ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
			defer cancel()

			_, err := applyMigrations(ctx, dsn, dir, func(name string, applied bool) {
				if applied {
					fmt.Printf("  apply  %s\n", name)
				} else {
					fmt.Printf("  skip   %s\n", name)
				}
			})
			return err
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "external migrations directory (default: embedded)")
	return cmd
}

// migrateTimeout bounds a full migration pass. Shared by the `migrate` command
// and the boot-time pass in `serve` so neither can hang a deploy indefinitely.
const migrateTimeout = 5 * time.Minute

type migration struct {
	name string
	body []byte
}

// applyMigrations brings the database at dsn up to date with the migration set
// (embedded when dir is empty) and returns how many it applied. onStatus, when
// non-nil, is called per migration in file order as it is decided, so a caller
// can report progress during a long pass.
//
// Every migration runs in its own transaction and is recorded in the same
// transaction, so a mid-set failure leaves the schema at the last fully applied
// version rather than half-way through one.
func applyMigrations(ctx context.Context, dsn, dir string, onStatus func(name string, applied bool)) (int, error) {
	// Load before dialing: a bad migration set fails without a DB round trip.
	files, err := loadMigrations(dir)
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, errors.New("no migrations found")
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)`); err != nil {
		return 0, fmt.Errorf("init migrations table: %w", err)
	}

	// One read for the whole set. Per-file EXISTS probes cost a round trip each,
	// which on the Supabase session pooler is minutes of boot latency once the
	// set is a couple hundred files long.
	done, err := appliedMigrations(ctx, conn)
	if err != nil {
		return 0, err
	}

	applied := 0
	for _, m := range files {
		if _, ok := done[m.name]; ok {
			if onStatus != nil {
				onStatus(m.name, false)
			}
			continue
		}
		if strings.TrimSpace(string(m.body)) == "" {
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, string(m.body)); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("apply %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations(name) VALUES($1)`, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied++
		if onStatus != nil {
			onStatus(m.name, true)
		}
	}
	return applied, nil
}

func appliedMigrations(ctx context.Context, conn *pgx.Conn) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

func loadMigrations(dir string) ([]migration, error) {
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		out := make([]migration, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			body, err := os.ReadFile(path.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, migration{name: e.Name(), body: body})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
		return out, nil
	}

	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(db.Migrations, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{name: e.Name(), body: body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
