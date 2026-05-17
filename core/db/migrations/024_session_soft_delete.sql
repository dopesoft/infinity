-- 024_session_soft_delete.sql - soft delete for sessions.
--
-- Adds `deleted_at` to mem_sessions so the boss can remove conversations
-- from the Sessions drawer without losing the underlying memories: the
-- observations stay (mem_observations.session_id has ON DELETE CASCADE,
-- but we intentionally don't cascade - soft delete preserves the trail
-- for cross-session memory + reflections + audit).
--
-- The partial index on (started_at DESC) WHERE deleted_at IS NULL keeps
-- the Sessions list query fast even once a lot of rows have been
-- tombstoned. handleSessions, handleSessionMessages, and
-- hydrateLoopSession all filter on `deleted_at IS NULL`.

BEGIN;

ALTER TABLE mem_sessions
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS mem_sessions_active_started_idx
    ON mem_sessions(started_at DESC)
    WHERE deleted_at IS NULL;

COMMIT;
