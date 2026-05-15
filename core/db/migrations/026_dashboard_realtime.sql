-- 026_dashboard_realtime.sql — add the dashboard mutation tables to the
-- supabase_realtime publication AND set REPLICA IDENTITY FULL on each.
--
-- Why: Studio's DashboardClient now subscribes via useRealtime to
-- mem_tasks, mem_pursuits, mem_pursuit_checkins, mem_followups, and
-- mem_saved so the page re-fetches when the agent mutates them (dismiss
-- a follow-up, check in on a habit, complete a task, etc.). Without
-- this migration the subscription is silent because:
--   1. The tables aren't in the publication, so the WAL stream never
--      carries their events at all.
--   2. Even if they were, default REPLICA IDENTITY only emits the PK on
--      UPDATE — Supabase Realtime can't evaluate RLS against a payload
--      missing user_id and drops the event silently. Same class of bug
--      migration 025 fixed for the other tables.
--
-- Idempotent: ALTER PUBLICATION ADD TABLE is guarded by a NOT EXISTS
-- check, and REPLICA IDENTITY FULL is a no-op when already set.

BEGIN;

DO $$
DECLARE
    tbl TEXT;
    dashboard_tables TEXT[] := ARRAY[
        'mem_tasks',
        'mem_pursuits',
        'mem_pursuit_checkins',
        'mem_followups',
        'mem_saved'
    ];
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        CREATE PUBLICATION supabase_realtime;
    END IF;

    FOREACH tbl IN ARRAY dashboard_tables LOOP
        -- Skip tables that don't exist yet (defensive against partial
        -- migrate state on a fresh install).
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.tables
            WHERE table_schema = 'public' AND table_name = tbl
        ) THEN
            CONTINUE;
        END IF;

        -- Add to publication if not already a member.
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
            WHERE pubname = 'supabase_realtime'
              AND schemaname = 'public'
              AND tablename = tbl
        ) THEN
            EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tbl);
        END IF;

        -- Full row in WAL so RLS can evaluate against UPDATE payloads.
        EXECUTE format('ALTER TABLE public.%I REPLICA IDENTITY FULL', tbl);
    END LOOP;
END $$;

INSERT INTO infinity_meta (key, value)
VALUES ('dashboard_realtime_added_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
