package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/settings"
	"github.com/dopesoft/infinity/core/internal/worldmodel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// backfill-circle — teach Jarvis the people he has already been speaking to.
//
// Calls only started entering memory once the capture seam existed
// (phone/memory.go). Every call BEFORE that lives on as a card and a transcript
// and nothing else: never observed, never compressed, never learned. So the boss
// has a wife, a brother and a cousin that Jarvis has literally phoned, and does
// not know exist.
//
// This is the one-shot that closes that gap. It is idempotent (an already-
// remembered call is skipped on its call_id), so it is safe to run twice.
//
//  1. every past call  → a session + an observation, through the same shape the
//     live path now writes, so it compresses and retrieves
//     like anything else
//  2. the phone book   → person entities (deterministic; if he has your number,
//     he knows you)
//  3. what was said    → durable facts about those people (LLM: relationships,
//     birthdays), written onto the entities
func newBackfillCircleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backfill-circle",
		Short: "Teach the world model the people Jarvis has already called (one-shot, idempotent)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return fmt.Errorf("DATABASE_URL is not set")
			}
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			store := memory.NewStore(pool)
			wm := worldmodel.NewStore(pool, slog.Default())

			calls, err := rememberPastCalls(ctx, pool, store)
			if err != nil {
				return err
			}
			fmt.Printf("  calls remembered: %d\n", calls)

			contacts, err := wm.SyncContacts(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("  people from the phone book: %d\n", contacts)

			// The facts (who they are to him, birthdays) need judgment, so they
			// need the model: the boss's ACTIVE one, same brain as chat.
			provider, perr := llm.FromEnv()
			if perr != nil {
				fmt.Printf("  (no LLM configured, so no facts learned: %v)\n", perr)
				return nil
			}
			drafter := &activeModelProvider{
				registry: llm.BuildRegistry(llm.NewOAuthStore(pool), llm.NewKeyStore(pool)),
				settings: settings.New(pool),
				fallback: provider,
			}
			rep, err := wm.ExtractPeopleFacts(ctx, drafter, 80)
			if err != nil {
				return err
			}
			fmt.Printf("  people learned: %d, durable facts: %d (from %d memories)\n",
				rep.People, rep.Facts, rep.Scanned)
			return nil
		},
	}
}

// rememberPastCalls turns every call card we already have into a real memory.
//
// The transcript is right there in the surface item's body: the call happened,
// it was recorded, it was simply never observed. Idempotent on call_id, so a
// second run adds nothing.
func rememberPastCalls(ctx context.Context, pool *pgxpool.Pool, store *memory.Store) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT external_id, title, COALESCE(body, ''), created_at
		FROM mem_surface_items
		WHERE surface = 'calls' AND source = 'phone'
		  AND COALESCE(body, '') <> ''
		ORDER BY created_at
	`)
	if err != nil {
		return 0, fmt.Errorf("read call cards: %w", err)
	}
	type call struct{ id, title, body string }
	var calls []call
	for rows.Next() {
		var c call
		var ignored any
		if err := rows.Scan(&c.id, &c.title, &c.body, &ignored); err != nil {
			rows.Close()
			return 0, err
		}
		calls = append(calls, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, c := range calls {
		// Already remembered? Skip. This is what makes a re-run safe.
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM mem_observations
				WHERE payload->>'call_id' = $1
			)`, c.id).Scan(&exists); err != nil {
			return n, err
		}
		if exists {
			continue
		}

		// kind='backfill', not 'user': this session is only the FK parent the
		// observation requires, never a conversation. The sessions list shows
		// kind='user', so stamping 'user' here put a dozen un-openable blank
		// rows in the boss's history (migration 184).
		sessionID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO mem_sessions (id, kind, origin_ref, started_at, ended_at)
			VALUES ($1::uuid, 'backfill', '{"kind":"phone_call","backfilled":true}'::jsonb, NOW(), NOW())
			ON CONFLICT (id) DO NOTHING`, sessionID); err != nil {
			return n, fmt.Errorf("open session for %s: %w", c.id, err)
		}

		// The card's body already reads as a narrative: "**Outcome:** …" then the
		// transcript. Prefixed with the title so the extractor knows who it was.
		text := c.title + ".\n\n" + strings.TrimSpace(c.body)
		cleaned, _ := memory.StripSecrets(text)

		var vec []float32
		if e := embed.FromEnv(); e != nil {
			if v, err := e.Embed(ctx, cleaned); err == nil {
				vec = v
			}
		}
		if _, err := store.InsertObservation(ctx, memory.ObservationInput{
			SessionID:  sessionID,
			HookName:   "phone_call_backfill",
			RawText:    cleaned,
			Embedding:  vec,
			Importance: 6,
			Payload: map[string]any{
				"kind":    "phone_call",
				"call_id": c.id,
			},
		}); err != nil {
			return n, fmt.Errorf("remember %s: %w", c.id, err)
		}
		n++
	}
	return n, nil
}
