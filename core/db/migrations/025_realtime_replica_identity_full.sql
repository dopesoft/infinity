-- 025_realtime_replica_identity_full.sql — set REPLICA IDENTITY FULL on the
-- realtime-published tables that the UI subscribes to for UPDATE events.
--
-- Why this is load-bearing: with the default REPLICA IDENTITY, Postgres
-- only writes the primary key to the WAL on UPDATE. Supabase Realtime
-- delivers what's in the WAL — so the user_id (and every other column)
-- is missing from the UPDATE payload. Realtime evaluates row-level
-- security against that payload, fails to find a user_id matching the
-- subscriber, and silently drops the event.
--
-- Symptom: a session's auto-generated name lands in the DB but the
-- Studio header doesn't refresh until the boss navigates away and back
-- (that path re-fetches via HTTP, which uses the full row + RLS just
-- fine). Same class of bug bites status flips on skill proposals,
-- trust contracts, heartbeats, etc. Setting FULL fixes the whole class.
--
-- Cost: each UPDATE writes the full prior row to the WAL instead of
-- just the PK — at our write volume (single-user, mostly INSERTs) this
-- is well within the noise. Idempotent: ALTER TABLE ... REPLICA
-- IDENTITY FULL is a no-op when already set.

BEGIN;

DO $$
DECLARE
    tbl TEXT;
    realtime_tables TEXT[] := ARRAY[
        'mem_sessions',
        'mem_observations',
        'mem_memories',
        'mem_summaries',
        'mem_audit',
        'mem_heartbeats',
        'mem_heartbeat_findings',
        'mem_intent_decisions',
        'mem_outcomes',
        'mem_trust_contracts',
        'mem_patterns',
        'mem_crons',
        'mem_sentinels',
        'mem_skills',
        'mem_skill_runs',
        'mem_skill_proposals',
        'mem_profiles',
        'mem_lessons',
        'mem_session_state',
        'mem_working_buffer'
    ];
BEGIN
    FOREACH tbl IN ARRAY realtime_tables LOOP
        IF EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = 'public' AND table_name = tbl
        ) THEN
            EXECUTE format('ALTER TABLE public.%I REPLICA IDENTITY FULL', tbl);
        END IF;
    END LOOP;
END $$;

COMMIT;
