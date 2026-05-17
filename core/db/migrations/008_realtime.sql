-- 008_realtime.sql - Add user-rooted tables to the supabase_realtime
-- publication so Studio can subscribe to INSERT/UPDATE/DELETE events.
--
-- The publication is created by Supabase out of the box; this migration
-- only adds tables to it. Idempotent: ALTER PUBLICATION ADD TABLE
-- skips tables already in the publication thanks to the DO block.

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
    -- Defensive: create the publication if it doesn't exist (it always
    -- does on Supabase, but local dev sometimes ships without it).
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        CREATE PUBLICATION supabase_realtime;
    END IF;

    FOREACH tbl IN ARRAY realtime_tables LOOP
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = 'public' AND table_name = tbl
        ) THEN
            CONTINUE;
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
            WHERE pubname = 'supabase_realtime'
              AND schemaname = 'public'
              AND tablename = tbl
        ) THEN
            EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tbl);
        END IF;
    END LOOP;
END $$;

INSERT INTO infinity_meta (key, value)
VALUES ('realtime_phase_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
