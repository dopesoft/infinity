-- 136_artifacts_realtime.sql - add mem_artifacts to the supabase_realtime
-- publication AND set REPLICA IDENTITY FULL, so the dashboard's "Made by
-- Jarvis" gallery updates live the instant Jarvis ships an artifact.
--
-- The dashboard's "Saved" card now surfaces mem_artifacts (via loadArtifacts).
-- DashboardClient already subscribes with useRealtime("*", refetch), so the
-- only missing link is the WAL stream: without the table in the publication,
-- an artifact_save never reaches the client and the gallery would only refresh
-- on the next manual load. Same fix migration 026 applied for the other
-- dashboard tables. REPLICA IDENTITY FULL emits the full row so Supabase
-- Realtime can evaluate RLS against UPDATE/DELETE payloads.
--
-- Idempotent: membership is guarded by a NOT EXISTS check and REPLICA IDENTITY
-- FULL is a no-op when already set.

BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        CREATE PUBLICATION supabase_realtime;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'mem_artifacts'
    ) THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
            WHERE pubname = 'supabase_realtime'
              AND schemaname = 'public'
              AND tablename = 'mem_artifacts'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_artifacts';
        END IF;

        EXECUTE 'ALTER TABLE public.mem_artifacts REPLICA IDENTITY FULL';
    END IF;
END $$;

INSERT INTO infinity_meta (key, value)
VALUES ('artifacts_realtime_added_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
