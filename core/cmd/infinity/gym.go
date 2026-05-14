package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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
