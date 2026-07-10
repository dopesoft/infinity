-- 167_agent_state.sql — durable keyed state for the agent + sentinel poll-state persistence.
--
-- Two related gaps, one migration:
--
--   1. mem_agent_state — a generic, agent-writable keyed state cell
--      (state_set / state_get / state_list / state_delete tools). This is
--      the missing "what did I see last time?" building block: standing
--      watches diff the world against their previous snapshot, recipes
--      checkpoint progress, counters accumulate — all without bespoke
--      per-feature tables (Rule #1: generic contract, not a vertical).
--
--   2. mem_sentinels.runtime_state — sentinel poll baselines (fire_on_change
--      keys, file modtimes) previously lived in a process-local map and
--      re-baselined on every deploy, so a change that happened across a
--      restart was silently missed. Persisting the baseline closes that hole.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_agent_state (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE mem_sentinels
    ADD COLUMN IF NOT EXISTS runtime_state JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMIT;
