-- 086_mem_watches.sql — the generic "watch a long action until it settles,
-- then report back into the chat" substrate.
--
-- Why this exists: when the agent says "I'll watch X and let you know how it
-- goes," it needs a DURABLE, DETERMINISTIC way to deliver that follow-up
-- AFTER the turn ends. The failure this fixes (2026-06-01): the agent fired a
-- cron, then launched a fire-and-forget `claude_code__Bash` poll on the Mac as
-- its "watcher." A backgrounded shell task can't wake the agent loop and has
-- no path back into the boss's chat, so the promised callback never came — the
-- boss waited forever for a message that was never going to fire.
--
-- The fix is the same shape as mem_runs (035): one generic contract, polled by
-- a Go ticker (deterministic — code, not LLM cognition), delivered through the
-- existing proactive_message path scoped to the originating session. The agent
-- ASSEMBLES "watch this and report back" from the `watch_until` tool over this
-- table; no per-target Go branches.
--
-- A watch resolves against mem_runs: kind='run' watches a specific run id,
-- kind='cron' watches the latest run of a cron. Terminal = mem_runs.status
-- leaves 'running' (becomes 'ok' or 'error'). On settle OR timeout the poller
-- pushes one proactive_message into session_id and marks the row done.
--
-- Idempotent: every block guards against re-running.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_watches (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      UUID NOT NULL,                    -- where the follow-up message is delivered
    kind            TEXT NOT NULL DEFAULT 'run'       -- 'run' (mem_runs id) | 'cron' (cron id)
                    CHECK (kind IN ('run','cron')),
    target_id       TEXT NOT NULL,                    -- mem_runs.id (kind=run) or cron id (kind=cron)
    target_label    TEXT NOT NULL DEFAULT '',         -- human label echoed in the report
    note            TEXT NOT NULL DEFAULT '',         -- optional extra context to echo back
    poll_seconds    INTEGER NOT NULL DEFAULT 8,       -- how often the poller re-checks
    status          TEXT NOT NULL DEFAULT 'pending'   -- 'pending' | 'settled' | 'expired' | 'cancelled'
                    CHECK (status IN ('pending','settled','expired','cancelled')),
    last_state      TEXT NOT NULL DEFAULT '',         -- last observed mem_runs.status
    outcome         TEXT NOT NULL DEFAULT '',         -- terminal status at settle ('ok' | 'error' | 'timeout')
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_poll_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 minutes',
    settled_at      TIMESTAMPTZ
);

-- Hot query: the poller pulls due, still-pending watches every tick.
CREATE INDEX IF NOT EXISTS idx_mem_watches_due
    ON mem_watches(next_poll_at) WHERE status = 'pending';

INSERT INTO infinity_meta (key, value)
VALUES ('mem_watches_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
