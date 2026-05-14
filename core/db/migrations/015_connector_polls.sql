-- 015_connector_polls.sql — enable Jarvis to schedule deterministic
-- connector polling (Gmail inbox, Google Calendar, etc.) via cron WITHOUT
-- spinning up the LLM. Phase 6 cron only supported agent-driven jobs
-- (system_event / isolated_agent_turn). This migration lets Jarvis say
-- "check email every 15 min" and have the scheduler fire a pure Go
-- handler that hits Composio's REST tools.execute endpoint, writes the
-- results into mem_followups / mem_calendar_events, and emits a hook so
-- the captures land in mem_observations alongside everything else.
--
-- Two changes:
--   1. mem_crons.job_kind grows a new value: 'connector_poll'.
--   2. mem_crons gets a target_config JSONB column for structured polling
--      parameters (toolkit, action slug, connected_account_id, max items,
--      destination table). target stays for backward-compat — agent jobs
--      keep using it as the literal prompt.
--
-- Idempotency for the actual poll output is enforced at write-time by the
-- poller (UNIQUE on (source, source_ref->>'remote_id') for mem_followups
-- and on (source, source_ref->>'remote_id') for mem_calendar_events) so a
-- re-fetch of the same Gmail message doesn't create a dupe row.

BEGIN;

-- ── job_kind: allow 'connector_poll' ──────────────────────────────────────
ALTER TABLE mem_crons DROP CONSTRAINT IF EXISTS mem_crons_job_kind_check;
ALTER TABLE mem_crons
    ADD CONSTRAINT mem_crons_job_kind_check
    CHECK (job_kind IN ('system_event', 'isolated_agent_turn', 'connector_poll'));

-- ── target_config: structured polling parameters ──────────────────────────
-- Shape (for connector_poll jobs):
--   {
--     "toolkit": "gmail",
--     "action":  "GMAIL_FETCH_EMAILS",
--     "connected_account_id": "ca_...",
--     "arguments": { "max_results": 10, "query": "is:unread" },
--     "sink": "followups" | "calendar"   -- which dashboard table to write
--   }
-- Empty {} for agent jobs (system_event / isolated_agent_turn) which
-- continue to read their prompt from the `target` text column.
ALTER TABLE mem_crons
    ADD COLUMN IF NOT EXISTS target_config JSONB NOT NULL DEFAULT '{}'::jsonb;

-- ── dedup indexes for the sink tables ─────────────────────────────────────
-- mem_followups stores its remote pointer in source_ref. We dedupe by the
-- (source, remote_id) tuple so a re-poll of the same message thread doesn't
-- duplicate. Partial unique index — only constrains rows that actually
-- carry a remote_id, so manual followups (source='other', no remote_id)
-- stay unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mem_followups_source_remote
    ON mem_followups (source, (source_ref->>'remote_id'))
    WHERE source_ref ? 'remote_id';

CREATE UNIQUE INDEX IF NOT EXISTS uq_mem_calendar_events_source_remote
    ON mem_calendar_events (source, (source_ref->>'remote_id'))
    WHERE source_ref ? 'remote_id';

COMMIT;
