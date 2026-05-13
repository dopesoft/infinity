package main

import (
	"context"
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

func reflectCmd() *cobra.Command {
	var (
		session string
		window  time.Duration
		limit   int
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "reflect",
		Short: "Run metacognition over recent sessions (Park-style reflection trees + MAR critic persona)",
		Long: `Walks recent sessions, asks Claude Haiku to critique each one, and writes the
result to mem_reflections. High-confidence lessons additionally promote into
mem_lessons so the existing search machinery picks them up.

By default reflects on every session in the last 24h that doesn't already
have a reflection. Pass --session <id> to target a single session, or
--force to overwrite an existing reflection.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			provider, err := llm.FromEnv()
			if err != nil {
				return fmt.Errorf("reflection needs an LLM provider: %w", err)
			}
			a, ok := provider.(*llm.Anthropic)
			if !ok {
				return errors.New("reflection currently requires LLM_PROVIDER=anthropic")
			}
			critic := llm.NewAnthropicCritic(a, os.Getenv("LLM_SUMMARIZE_MODEL"))
			reflector := memory.NewReflector(pool, embed.FromEnv(), memory.NewCritic(critic))

			if session != "" {
				id, err := reflector.ReflectOnSession(ctx, session, force)
				if err != nil {
					return err
				}
				if id == "" {
					fmt.Println("session reflected: no transcript content, skipped")
					return nil
				}
				fmt.Printf("reflected session=%s reflection_id=%s\n", session, id)
				return nil
			}

			processed, err := reflector.ReflectRecent(ctx, window, limit)
			if err != nil {
				return err
			}
			fmt.Printf("reflected %d session(s) over the last %s\n", processed, window)
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "reflect on this single session id (UUID)")
	cmd.Flags().DurationVar(&window, "window", 24*time.Hour, "look back this far for sessions without reflections")
	cmd.Flags().IntVar(&limit, "limit", 20, "max sessions to reflect on per run")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing reflection for the same session")
	return cmd
}
