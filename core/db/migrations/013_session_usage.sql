-- 013_session_usage.sql — Persist API-reported token usage on the session.
--
-- The agent loop records every successful turn's Usage.Input / Usage.Output
-- onto Session.{last,total}{Input,Output}Tokens. Those fields lived only in
-- process memory, so a Railway restart wiped them — the context meter in
-- Studio would then show 0% on a session that very much was not empty.
--
-- Adding the counters to mem_sessions lets the agent hydrate them when a
-- session is faulted back into the in-memory map after a restart, and
-- write them back after each successful turn. Best-effort on both sides:
-- if the persistence layer fails, the loop keeps running and the meter
-- still reflects whatever the current process knows.
--
-- Defaults to 0 so existing rows backfill cleanly without a migration of
-- their own. The values catch up on the next turn that fires through the
-- loop for each session.
BEGIN;

ALTER TABLE mem_sessions
    ADD COLUMN IF NOT EXISTS last_input_tokens    INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_output_tokens   INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_input_tokens   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_output_tokens  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_updated_at     TIMESTAMPTZ;

INSERT INTO infinity_meta (key, value)
VALUES ('session_usage_persisted_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
