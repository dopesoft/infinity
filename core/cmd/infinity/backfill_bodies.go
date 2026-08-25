package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// backfill-bodies — fill in the message bodies behind already-surfaced cards.
//
// `infinity serve` runs the same sweep on a ticker, so this command exists for
// the two cases a ticker can't cover: draining a large existing backlog on
// demand (without waiting out the tick interval), and checking convergence with
// real numbers rather than assuming.
//
// Read-only against the mail provider: it fetches messages the boss has already
// been shown a card for, and writes the body onto that card's own row.
func backfillBodiesCmd() *cobra.Command {
	var (
		batch  int
		rounds int
		dbURL  string
	)
	cmd := &cobra.Command{
		Use:   "backfill-bodies",
		Short: "Fetch and store the real message body for surfaced follow-ups that have none",
		Long: `Fills cached_html / cached_text (and the derived preview) on open follow-up
email rows in mem_surface_items that were surfaced without a body.

Rows whose account is gone stop being retried after 3 recorded attempts; the
reason stays on the row so it is visible rather than silently skipped.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer cancel()

			if dbURL == "" {
				dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
			}
			if dbURL == "" {
				return fmt.Errorf("DATABASE_URL is not set")
			}
			pool, err := pgxpool.New(ctx, dbURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			key := func() string {
				if v := strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY")); v != "" {
					return v
				}
				return strings.TrimSpace(os.Getenv("COMPOSIO_ADMIN_API_KEY"))
			}
			if key() == "" {
				return fmt.Errorf("COMPOSIO_API_KEY is not set, so there is nothing to fetch messages through")
			}
			// The cache resolves each row's stored account hint to a live
			// connected account + entity, including redirecting a revoked
			// account to its reconnected twin. Prime it once; no ticker needed
			// for a one-shot run.
			cache := connectors.New(pool, key)
			if err := cache.Refresh(ctx); err != nil {
				return fmt.Errorf("connector refresh: %w", err)
			}
			surface.SetBodyFetcher(connectors.NewMessageFetcher(connectors.NewExecuteClient(key), cache))

			b := surface.NewBodyBackfiller(pool)
			if b == nil {
				return fmt.Errorf("could not open the surface store")
			}
			totalFilled, totalFailed := 0, 0
			for i := 1; i <= rounds; i++ {
				res, err := b.Run(ctx, batch)
				if err != nil {
					return err
				}
				totalFilled += res.Filled
				totalFailed += res.Failed
				fmt.Printf("round %d: scanned %d, filled %d, derived %d, failed %d, still pending %d\n",
					i, res.Scanned, res.Filled, res.Derived, res.Failed, res.Remaining)
				if (res.Scanned == 0 && res.Derived == 0) || res.Remaining == 0 {
					break
				}
			}
			fmt.Printf("done: %d bodies stored, %d could not be fetched\n", totalFilled, totalFailed)
			return nil
		},
	}
	cmd.Flags().IntVar(&batch, "batch", 25, "messages fetched per round")
	cmd.Flags().IntVar(&rounds, "rounds", 40, "maximum rounds before stopping")
	cmd.Flags().StringVar(&dbURL, "db", "", "database URL (defaults to $DATABASE_URL)")
	return cmd
}
