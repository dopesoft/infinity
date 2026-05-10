package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

func migrateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply SQL migrations from db/migrations against $DATABASE_URL",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return fmt.Errorf("DATABASE_URL is required")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

			files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
			if err != nil {
				return err
			}
			sort.Strings(files)
			if len(files) == 0 {
				return fmt.Errorf("no .sql files in %s", dir)
			}

			for _, path := range files {
				name := filepath.Base(path)
				var exists bool
				if err := conn.QueryRow(ctx,
					`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, name,
				).Scan(&exists); err != nil {
					return err
				}
				if exists {
					fmt.Printf("  skip   %s\n", name)
					continue
				}

				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if strings.TrimSpace(string(body)) == "" {
					continue
				}

				tx, err := conn.Begin(ctx)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, string(body)); err != nil {
					_ = tx.Rollback(ctx)
					return fmt.Errorf("apply %s: %w", name, err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
					_ = tx.Rollback(ctx)
					return err
				}
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				fmt.Printf("  apply  %s\n", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "db/migrations", "migrations directory")
	return cmd
}
