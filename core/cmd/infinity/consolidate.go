package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func consolidateCmd() *cobra.Command {
	var dryRun bool
	var compress bool
	var batch int
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Run nightly memory maintenance (decay, hot-reset, cluster, auto-forget; --compress to also promote observations)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			out := map[string]any{}

			if dryRun {
				report, err := memory.RunAutoForget(ctx, pool, true)
				if err != nil {
					return err
				}
				out["forget_dry_run"] = report
				return printJSON(out)
			}

			if compress {
				provider, err := llm.FromEnv()
				if err != nil {
					return fmt.Errorf("compression needs an LLM provider: %w", err)
				}
				a, ok := provider.(*llm.Anthropic)
				if !ok {
					return errors.New("compression currently requires LLM_PROVIDER=anthropic")
				}
				summarizer := llm.NewAnthropicSummarizer(a, os.Getenv("LLM_SUMMARIZE_MODEL"))
				comp := memory.NewCompressor(pool, embed.FromEnv(), memory.NewSummarizer(summarizer))

				processed, err := comp.CompressNewObservations(ctx, batch)
				if err != nil {
					return err
				}
				out["compressed"] = processed
			}

			report, err := memory.ConsolidateNightly(ctx, pool)
			if err != nil {
				return err
			}
			out["consolidate"] = report
			return printJSON(out)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview auto-forget without deleting")
	cmd.Flags().BoolVar(&compress, "compress", false, "also LLM-compress recent uncompressed observations into mem_memories")
	cmd.Flags().IntVar(&batch, "batch", 50, "max observations to compress per run when --compress")
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
