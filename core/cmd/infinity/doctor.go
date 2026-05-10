package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

type check struct {
	name string
	run  func(ctx context.Context) error
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, database, and runtime dependencies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			checks := []check{
				{"env: ANTHROPIC_API_KEY", func(_ context.Context) error {
					if os.Getenv("ANTHROPIC_API_KEY") == "" {
						return fmt.Errorf("not set")
					}
					return nil
				}},
				{"env: DATABASE_URL", func(_ context.Context) error {
					if os.Getenv("DATABASE_URL") == "" {
						return fmt.Errorf("not set")
					}
					return nil
				}},
				{"db: ping", func(ctx context.Context) error {
					dsn := os.Getenv("DATABASE_URL")
					if dsn == "" {
						return fmt.Errorf("skipped (no DATABASE_URL)")
					}
					conn, err := pgx.Connect(ctx, dsn)
					if err != nil {
						return err
					}
					defer conn.Close(ctx)
					return conn.Ping(ctx)
				}},
				{"db: pgvector extension", func(ctx context.Context) error {
					dsn := os.Getenv("DATABASE_URL")
					if dsn == "" {
						return fmt.Errorf("skipped (no DATABASE_URL)")
					}
					conn, err := pgx.Connect(ctx, dsn)
					if err != nil {
						return err
					}
					defer conn.Close(ctx)
					var installed bool
					err = conn.QueryRow(ctx,
						`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')`,
					).Scan(&installed)
					if err != nil {
						return err
					}
					if !installed {
						return fmt.Errorf("vector extension not installed (run: infinity migrate)")
					}
					return nil
				}},
			}

			ok := true
			for _, c := range checks {
				err := c.run(ctx)
				if err != nil {
					ok = false
					fmt.Printf("  FAIL   %-32s  %s\n", c.name, err)
				} else {
					fmt.Printf("  OK     %s\n", c.name)
				}
			}
			if !ok {
				return fmt.Errorf("one or more checks failed")
			}
			return nil
		},
	}
}
