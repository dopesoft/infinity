-- 027_curiosity_realtime.sql - add mem_curiosity_questions to the
-- supabase_realtime publication so the dashboard's "Questions" card
-- updates the instant the agent dismisses (via question_decide) or a
-- new question lands. Without this the card was a snapshot from initial
-- page load and dismissals only became visible on hard refresh.
--
-- Idempotent: ALTER PUBLICATION ADD TABLE is guarded by a NOT EXISTS
-- check, REPLICA IDENTITY FULL is a no-op if already set.

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
        WHERE table_schema = 'public' AND table_name = 'mem_curiosity_questions'
    ) THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
            WHERE pubname = 'supabase_realtime'
              AND schemaname = 'public'
              AND tablename = 'mem_curiosity_questions'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_curiosity_questions;
        END IF;

        ALTER TABLE public.mem_curiosity_questions REPLICA IDENTITY FULL;
    END IF;
END $$;

INSERT INTO infinity_meta (key, value)
VALUES ('curiosity_realtime_added_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
