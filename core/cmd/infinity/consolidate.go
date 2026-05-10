package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func consolidateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Run the nightly memory consolidation pass (decay, hot-reset, cluster, auto-forget)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return fmt.Errorf("DATABASE_URL is required")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			if dryRun {
				report, err := memory.RunAutoForget(ctx, pool, true)
				if err != nil {
					return err
				}
				return printJSON(map[string]any{"forget_dry_run": report})
			}

			report, err := memory.ConsolidateNightly(ctx, pool)
			if err != nil {
				return err
			}
			return printJSON(report)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview auto-forget without deleting")
	return cmd
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
