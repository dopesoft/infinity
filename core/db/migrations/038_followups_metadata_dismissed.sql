-- 038_followups_metadata_dismissed.sql - generic chip metadata + a real
-- "dismissed" lifecycle for follow-ups.
--
-- Why this exists:
--   1. Per Rule #1 the dashboard chips for follow-ups (account / intent /
--      mode) cannot be a fan of bespoke columns. They live in a generic
--      JSONB blob the agent (triage skills, connectors, cron recipes)
--      populates with whatever signals it judged. Studio reads
--      metadata.intent / metadata.mode and renders chips when present.
--      A new chip the agent invents shows up with zero schema work.
--
--   2. status='done' was overloaded as both "boss replied" and "boss
--      dismissed". The dashboard hid both, but they're semantically
--      different (a dismissal should NEVER come back even if the
--      connector re-polls the same message; a reply may want resurfacing
--      if the thread continues). We introduce a real 'dismissed' status
--      + dismissed_at timestamp so the boss's intent is durable.
--
--   3. The poller already does ON CONFLICT (source, remote_id) DO NOTHING,
--      so once a row is marked 'dismissed' it stays dismissed across
--      every future re-poll. This migration just gives the lifecycle a
--      proper name and a per-row audit timestamp.
--
-- Idempotent.

BEGIN;

ALTER TABLE mem_followups
    ADD COLUMN IF NOT EXISTS metadata     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS dismissed_at TIMESTAMPTZ;

-- Realtime publication is already configured on mem_followups in
-- migration 026; the new columns flow through automatically.

COMMIT;
