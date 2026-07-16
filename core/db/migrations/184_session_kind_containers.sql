-- 184_session_kind_containers.sql - blank sessions in the boss's Sessions tab.
--
-- Migration 040 introduced mem_sessions.kind precisely so background machinery
-- (cron / heartbeat / voyager / sentinel) stays out of the default sessions
-- list, which shows kind='user' and nothing else (server/api.go handleSessions).
-- Two callers written AFTER 040 never got the memo, and both surface as a blank
-- session the boss has to look at and delete:
--
--  1. backfill-circle (cmd/infinity/backfill_circle.go) mints one session per
--     past phone call, purely as the FK parent an observation requires, then
--     hangs the transcript off it as hook_name='phone_call_backfill'. It stamped
--     kind='user'. The sessions list therefore renders 12 of them as if they
--     were chats; the message renderer whitelists five hook names and
--     'phone_call_backfill' is not one, so every one of them opens EMPTY.
--     The container was never meant to be browsable. Retro-fixed below.
--
--  2. The skill-verification harness (voyager/harness.go markTestSession) tried
--     to stamp kind='skill_test' on its ephemeral session so verification would
--     stay invisible. 'skill_test' was never added to this constraint, so that
--     INSERT has ALWAYS failed the CHECK -- and the error is swallowed by
--     `_, _ = m.pool.Exec(...)`. With no row pre-stamped, the verify turn's own
--     memory.Store.OpenSession creates it with the column default: kind='user'.
--     Every skill verification has been popping a phantom blank session into the
--     boss's list for the length of the run. The ordering in harness.go is
--     already correct (mark BEFORE runTurn, and OpenSession's ON CONFLICT does
--     not touch kind), so widening the constraint is the whole fix -- no Go
--     change needed there.
--
-- Both are the same defect: a session that is a CONTAINER, not a conversation,
-- must never be kind='user'. Widen the allowed set to name the two container
-- kinds, then relabel the rows already sitting in his list.
--
-- Idempotent.

BEGIN;

-- ── 1. Name the container kinds ──────────────────────────────────────────────
-- Widening only: every row that satisfied the old set satisfies the new one, so
-- VALIDATE cannot fail. NOT VALID + VALIDATE keeps the scan at SHARE UPDATE
-- EXCLUSIVE rather than locking the table, matching 040's pattern.
ALTER TABLE mem_sessions DROP CONSTRAINT IF EXISTS mem_sessions_kind_check;

ALTER TABLE mem_sessions
    ADD CONSTRAINT mem_sessions_kind_check
    CHECK (kind IN ('user', 'cron', 'heartbeat', 'voyager', 'sentinel',
                    'workflow', 'backfill', 'skill_test'))
    NOT VALID;

ALTER TABLE mem_sessions VALIDATE CONSTRAINT mem_sessions_kind_check;

-- ── 2. Relabel the containers already in the list ────────────────────────────
-- The 12 rows backfill-circle wrote on 2026-07-13. Keyed on origin_ref, which
-- that command stamps as {"kind":"phone_call","backfilled":true}, so this only
-- ever touches its own output. Deliberately NOT a delete: the observation
-- hanging off each row is the call transcript the backfill existed to persist,
-- and mem_observations.session_id CASCADEs. Flipping kind hides the container
-- from the list while leaving the memory and its FK parent intact.
UPDATE mem_sessions
   SET kind = 'backfill'
 WHERE kind = 'user'
   AND origin_ref->>'backfilled' = 'true'
   AND origin_ref->>'kind' = 'phone_call';

COMMIT;
