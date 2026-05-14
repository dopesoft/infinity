-- 021_initiative.sql — initiative + economics.
--
-- Phase 6 (final) of the assembly substrate. An always-on agent needs two
-- things a reactive chatbot doesn't:
--   1. INITIATIVE — a policy for WHEN and HOW to reach the boss (push to
--      the phone now? surface a dashboard card? batch into a digest?), and
--      a ledger of what it sent.
--   2. ECONOMICS — awareness of what it costs to run, so it can make
--      value/cost tradeoffs instead of burning the budget blind.
--
-- Plus dependency-aware scheduling: a workflow run can wait on another run
-- finishing before the engine claims it.
--
--   mem_notifications            — the outbound ledger
--   mem_cost_events              — the cost ledger
--   mem_workflow_runs.depends_on — run-after-run ordering
--
-- The agent reaches out via the notify tool / records spend via cost_record
-- / reads the budget via budget_status — never raw SQL.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_notifications (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    urgency      TEXT NOT NULL DEFAULT 'normal',  -- urgent | normal | low
    title        TEXT NOT NULL,
    body         TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT 'agent',   -- skill / workflow / cron / agent
    -- How it was routed: push (sent to the phone), surface (dashboard
    -- card), digest (held for the next batch).
    channel      TEXT NOT NULL DEFAULT 'surface',
    -- sent → delivered · batched → waiting for the next digest flush
    status       TEXT NOT NULL DEFAULT 'sent',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mem_notifications_batched
    ON mem_notifications (created_at)
    WHERE status = 'batched';
CREATE INDEX IF NOT EXISTS idx_mem_notifications_recent
    ON mem_notifications (created_at DESC);

CREATE TABLE IF NOT EXISTS mem_cost_events (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    category   TEXT NOT NULL,                    -- llm | api | tool | workflow | …
    subject    TEXT NOT NULL DEFAULT '',         -- model name / tool name / …
    cost_usd   NUMERIC NOT NULL DEFAULT 0,       -- estimated USD
    units      TEXT NOT NULL DEFAULT '',         -- tokens | calls | …
    quantity   NUMERIC NOT NULL DEFAULT 0,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_cost_events_recent
    ON mem_cost_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_cost_events_category
    ON mem_cost_events (category, created_at DESC);

-- Dependency-aware scheduling: a run can wait on another run finishing.
-- The workflow engine's ClaimRunnable skips a run whose dependency isn't
-- 'done' yet.
ALTER TABLE mem_workflow_runs
    ADD COLUMN IF NOT EXISTS depends_on UUID REFERENCES mem_workflow_runs(id) ON DELETE SET NULL;

-- Realtime: Studio subscribes so the notification + cost ledgers live-update.
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        FOREACH tname IN ARRAY ARRAY['mem_notifications', 'mem_cost_events']
        LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_publication_tables
                 WHERE pubname = 'supabase_realtime'
                   AND schemaname = 'public'
                   AND tablename = tname
            ) THEN
                EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tname);
            END IF;
        END LOOP;
    END IF;
END $$;

COMMIT;
