package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/sessions"
)

// backfill-titles — give a title to every session that never got one.
//
// `infinity serve` runs the same sweep on a ticker, so this exists for the case
// a ticker is bad at: draining a backlog that has been building for months,
// right now, and reporting real numbers instead of "it'll happen eventually".
//
// Only sessions with something to render are titled. A bookkeeping container
// with no conversation has nothing to read, is already hidden from the boss's
// list, and gets a deterministic label at display time instead.
func backfillTitlesCmd() *cobra.Command {
	var (
		batch  int
		rounds int
		dbURL  string
	)
	cmd := &cobra.Command{
		Use:   "backfill-titles",
		Short: "Title sessions the auto-namer missed",
		Long: `Finds sessions with a readable transcript and no name, drafts a title for each
with the configured model, and records it.

Attempts are capped per session (3) and recorded on the row, so a session that
cannot be titled stops consuming calls instead of being retried forever.`,
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

			// Same provider resolution serve uses: the OAuth-backed provider
			// needs the pool for its token store, so it can't come from
			// llm.FromEnv.
			var provider llm.Provider
			if llm.IsOpenAIOAuth() {
				provider = llm.WrapNoDashes(llm.NewOpenAIOAuth(llm.NewOAuthStore(pool), llm.ModelForVendor("openai_oauth")))
			} else {
				provider, err = llm.FromEnv()
				if err != nil {
					return fmt.Errorf("model provider: %w", err)
				}
			}

			namer := sessions.NewNamer(pool, provider, os.Getenv("INFINITY_SESSION_NAME_MODEL"))
			total, failed := 0, 0
			for i := 1; i <= rounds; i++ {
				res, err := namer.SweepUnnamed(ctx, batch)
				if err != nil {
					return err
				}
				total += res.Named
				failed += res.Failed
				fmt.Printf("round %d: scanned %d, titled %d, failed %d, still untitled %d\n",
					i, res.Scanned, res.Named, res.Failed, res.Remaining)
				if res.Scanned == 0 || res.Remaining == 0 {
					break
				}
			}
			fmt.Printf("done: %d titled, %d could not be\n", total, failed)
			return nil
		},
	}
	cmd.Flags().IntVar(&batch, "batch", 20, "sessions titled per round")
	cmd.Flags().IntVar(&rounds, "rounds", 20, "maximum rounds before stopping")
	cmd.Flags().StringVar(&dbURL, "db", "", "database URL (defaults to $DATABASE_URL)")
	return cmd
}
