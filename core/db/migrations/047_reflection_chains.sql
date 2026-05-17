-- 047_reflection_chains.sql - cross-session reflection meta-lessons.
--
-- Reflection rows critique one session. Chains cluster repeated lessons across
-- sessions so Infinity can see how its behavior is changing over time.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_reflection_chains (
    id                    UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    topic                 TEXT NOT NULL UNIQUE,
    lesson                TEXT NOT NULL,
    source_reflection_ids UUID[] NOT NULL DEFAULT ARRAY[]::UUID[],
    occurrences           INT NOT NULL DEFAULT 0,
    confidence            DOUBLE PRECISION NOT NULL DEFAULT 0,
    first_seen_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_reflection_chains_recent
    ON mem_reflection_chains (updated_at DESC);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_reflection_chains'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_reflection_chains;
        END IF;
    END IF;
END $$;

COMMIT;
