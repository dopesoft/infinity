package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func gymCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gym",
		Short: "Gym / plasticity utilities",
	}
	cmd.AddCommand(gymExtractCmd())
	cmd.AddCommand(gymRescoreCmd())
	return cmd
}

// gymRescoreCmd repairs the training corpus after an outcome-classifier change:
// re-score every resolved prediction with the current SurpriseFor logic, purge
// prediction-sourced training examples whose source no longer qualifies, then
// re-mine. One-shot maintenance, idempotent (a clean system re-runs to all-zero).
func gymRescoreCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "rescore",
		Short: "Re-score historical predictions with the current classifier, then purge + re-mine stale training examples",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			rep, err := memory.RescorePredictions(ctx, pool)
			if err != nil {
				return fmt.Errorf("rescore: %w", err)
			}
			fmt.Printf("rescore: scanned=%d changed=%d was_high=%d now_high=%d dropped=%d\n",
				rep.Scanned, rep.Changed, rep.WasHigh, rep.NowHigh, rep.Dropped)

			store := plasticity.NewStore(pool)
			purged, err := store.PurgeStalePredictionExamples(ctx)
			if err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			fmt.Printf("purge: deleted_stale_examples=%d\n", purged)

			result, err := store.ExtractExamples(ctx, limit)
			if err != nil {
				return fmt.Errorf("re-mine: %w", err)
			}
			fmt.Printf("re-mine: inserted=%d evals=%d reflections=%d surprises=%d\n",
				result.Inserted, result.Evals, result.Lessons, result.Surprise)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 200, "max rows per source to inspect during re-mine")
	return cmd
}

func gymExtractCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract provenance-backed training examples into mem_training_examples",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			result, err := plasticity.NewStore(pool).ExtractExamples(ctx, limit)
			if err != nil {
				return err
			}
			fmt.Printf("inserted=%d evals=%d reflections=%d surprises=%d\n",
				result.Inserted, result.Evals, result.Lessons, result.Surprise)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "max rows per source to inspect")
	return cmd
}
