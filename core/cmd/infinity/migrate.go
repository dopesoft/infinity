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

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer conn.Close(ctx)

			if _, err := conn.Exec(ctx, `
				CREATE TABLE IF NOT EXISTS schema_migrations (
					name TEXT PRIMARY KEY,
					applied_at TIMESTAMPTZ DEFAULT NOW()
				)`); err != nil {
				return fmt.Errorf("init migrations table: %w", err)
			}

			files, err := loadMigrations(dir)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return errors.New("no migrations found")
			}

			for _, m := range files {
				var exists bool
				if err := conn.QueryRow(ctx,
					`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, m.name,
				).Scan(&exists); err != nil {
					return err
				}
				if exists {
					fmt.Printf("  skip   %s\n", m.name)
					continue
				}
				if strings.TrimSpace(string(m.body)) == "" {
					continue
				}

				tx, err := conn.Begin(ctx)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, string(m.body)); err != nil {
					_ = tx.Rollback(ctx)
					return fmt.Errorf("apply %s: %w", m.name, err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO schema_migrations(name) VALUES($1)`, m.name); err != nil {
					_ = tx.Rollback(ctx)
					return err
				}
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				fmt.Printf("  apply  %s\n", m.name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "external migrations directory (default: embedded)")
	return cmd
}

type migration struct {
	name string
	body []byte
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
