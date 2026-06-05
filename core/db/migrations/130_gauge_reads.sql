-- 130_gauge_reads.sql — the Gauge: up-front effort sizing of a task.
--
-- Before doing the work, it helps to know how much work it deserves. The Gauge
-- is a cheap classifier that reads each substantive turn and sizes it —
-- glance / standard / deep — the same shape as the intent detector. It runs
-- ASYNC (off the chat hot path; the boss is latency-sensitive) so turn-1 sizing
-- is advisory + observable, and a session-scoped provider feeds the read back
-- into multi-turn work so longer tasks calibrate (deep → "open a Mandate").
--
-- This table is the durable record + the Studio chip's source.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_gauge_reads (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id   UUID,
    -- a short excerpt of the turn that was sized (for the chip tooltip / audit).
    turn_excerpt TEXT NOT NULL DEFAULT '',
    -- glance | standard | deep
    tier         TEXT NOT NULL DEFAULT 'standard'
                 CHECK (tier IN ('glance','standard','deep')),
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_gauge_reads_session
    ON mem_gauge_reads (session_id, created_at DESC);

-- Realtime: the Live + Heartbeat gauge chip live-updates.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_gauge_reads'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_gauge_reads;
        END IF;
    END IF;
END $$;

COMMIT;
