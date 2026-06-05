-- 128_compass.sql — the Compass: the boss's own, AUTHORED north-star.
--
-- mem_memories (boss profile) and Honcho both model the boss from what we
-- OBSERVE. Neither captures what the boss DECLARES as his mission, his current
-- priorities, and the principles he wants Jarvis to operate under — in his own
-- words. That authored intent is the highest-signal context there is, and it
-- was missing. The Compass is that document: a handful of free-text sections the
-- boss edits in Settings, injected into every turn by compass.Provider so every
-- answer is framed by what actually matters to him.
--
-- One thin table: one row per section, ordered. The agent reads it (via the
-- provider) but does not write it in v1 — it is the boss's voice, not an
-- inference.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_compass (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- mission | goals | challenges | principles | fronts — free-form so the
    -- boss can add a new kind of section without a migration, but the Studio
    -- editor seeds these five.
    section    TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    -- display + injection order (lower first).
    position   INT  NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (section)
);

CREATE INDEX IF NOT EXISTS idx_mem_compass_position
    ON mem_compass (position ASC);

-- Realtime: Studio's Compass editor live-updates across devices.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_compass'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_compass;
        END IF;
    END IF;
END $$;

COMMIT;
