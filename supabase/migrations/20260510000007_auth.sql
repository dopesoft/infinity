-- 007_auth.sql — Authentication: single-user owner model.
--
-- Phase 0 of multi-tenancy. Adds nullable user_id to every user-rooted table
-- so future inserts carry the authenticated owner's UUID. The single-user
-- gate is enforced in Go: only requests whose JWT subject matches
-- infinity_meta.owner_user_id reach the API. Existing rows with NULL user_id
-- belong to the owner by convention (the carry-over).
--
-- Going multi-tenant later is a strict superset:
--   1. Drop the "must equal owner_user_id" check in core/internal/auth.
--   2. Backfill NULL user_ids to the owner via a one-line UPDATE.
--   3. Flip user_id columns to NOT NULL, add RLS policies.
-- No schema breakage.

BEGIN;

-- ---------------------------------------------------------------------------
-- Owner pointer: set once on first signup, then immutable.
-- Stored in infinity_meta to avoid a one-row table.
-- ---------------------------------------------------------------------------
-- (no schema change; just convention. The Go service writes/reads the
-- key 'owner_user_id' against the existing infinity_meta table.)

-- ---------------------------------------------------------------------------
-- user_id columns. Nullable + indexed. FK to auth.users only when that schema
-- exists (it will on Supabase; gracefully skipped on plain Postgres dev DBs).
-- ---------------------------------------------------------------------------

DO $$
DECLARE
    auth_users_exists BOOLEAN;
    tbl TEXT;
    user_rooted_tables TEXT[] := ARRAY[
        'mem_sessions',
        'mem_observations',
        'mem_memories',
        'mem_summaries',
        'mem_lessons',
        'mem_audit',
        'mem_profiles',
        'mem_crons',
        'mem_sentinels',
        'mem_skills',
        'mem_skill_runs',
        'mem_skill_proposals',
        'mem_skill_failures',
        'mem_intent_decisions',
        'mem_heartbeats',
        'mem_outcomes',
        'mem_trust_contracts',
        'mem_patterns',
        'mem_session_state',
        'mem_working_buffer'
    ];
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'auth' AND table_name = 'users'
    ) INTO auth_users_exists;

    FOREACH tbl IN ARRAY user_rooted_tables LOOP
        -- Skip tables that don't exist (defensive — schema variance across envs).
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = 'public' AND table_name = tbl
        ) THEN
            CONTINUE;
        END IF;

        -- Add user_id if missing.
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = tbl AND column_name = 'user_id'
        ) THEN
            EXECUTE format('ALTER TABLE public.%I ADD COLUMN user_id UUID', tbl);
            EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON public.%I(user_id)',
                           tbl || '_user_id_idx', tbl);
        END IF;

        -- Attach FK to auth.users when on Supabase.
        IF auth_users_exists AND NOT EXISTS (
            SELECT 1 FROM information_schema.table_constraints
            WHERE table_schema = 'public'
              AND table_name = tbl
              AND constraint_name = tbl || '_user_id_fkey'
        ) THEN
            BEGIN
                EXECUTE format(
                    'ALTER TABLE public.%I ADD CONSTRAINT %I FOREIGN KEY (user_id) REFERENCES auth.users(id) ON DELETE CASCADE',
                    tbl, tbl || '_user_id_fkey'
                );
            EXCEPTION WHEN insufficient_privilege THEN
                -- Supabase may restrict cross-schema FK creation from public; degrade gracefully.
                RAISE NOTICE 'skipped FK on %.user_id (insufficient_privilege)', tbl;
            END;
        END IF;
    END LOOP;
END $$;

-- Sanity: record migration intent so doctor reports auth phase.
INSERT INTO infinity_meta (key, value)
VALUES ('auth_phase_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
