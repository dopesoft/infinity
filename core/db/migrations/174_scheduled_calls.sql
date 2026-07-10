-- 174_scheduled_calls.sql
--
-- One-shot scheduled phone calls ("call the dentist tomorrow at 3"). Cron is
-- recurring-only and cannot express a precise datetime or self-terminate, so
-- scheduled calls get their own fire-once table, modeled on the watch.Poller
-- shape: a poller selects due rows and a status-guarded UPDATE guarantees
-- exactly one dispatch even across restarts / concurrency. Also backs the
-- automatic retry on a no-answer/busy.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_scheduled_calls (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payload    JSONB       NOT NULL,              -- the full Brief (to, name, topic, goal, constraints)
    fire_at    TIMESTAMPTZ NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'pending', -- pending | firing | fired | failed | canceled
    attempts   INT         NOT NULL DEFAULT 0,
    note       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_scheduled_calls_due
    ON mem_scheduled_calls (fire_at)
    WHERE status = 'pending';

COMMIT;
