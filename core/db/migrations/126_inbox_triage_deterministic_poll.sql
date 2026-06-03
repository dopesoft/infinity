-- 126_inbox_triage_deterministic_poll.sql — convert inbox-triage from an
-- LLM agent turn to a deterministic Composio poll.
--
-- The isolated_agent_turn version had the gpt-5.x brain orchestrate a tool-
-- loading dance (skills_invoke → load_tools → call) that failed every way it
-- could: load-loops, TTL decay evicting tools before use, loop-guard blocks. It
-- NEVER reliably surfaced an email. The boss is right: this should be a
-- deterministic skill, like his africa CRM's Python skills.
--
-- Infinity already has that path — the connector_poll cron kind runs poller.go
-- directly (no LLM, no agent loop): Composio GMAIL_FETCH_EMAILS → parse →
-- mem_followups → the Follow-ups dashboard (HTML body fetched live on open).
-- The follow-up triager then sets the needs-reply/intent chips with the boss's
-- SETTINGS-selected model (not hardcoded Haiku), async + non-blocking.
--
-- all_active=true makes the poll fan out across EVERY currently-active Gmail
-- account discovered live from the connectors cache — reconnect-proof, never a
-- hardcoded ca_ id (the husk footgun that's bitten us repeatedly).
--
-- Requires the matching code (poller all_active + SetCache) to be deployed; the
-- running scheduler reloads crons at boot and picks up the new kind.
-- Idempotent; guarded by name.

BEGIN;

UPDATE mem_crons
   SET job_kind     = 'connector_poll',
       target       = '',
       target_config = jsonb_build_object(
         'toolkit',    'gmail',
         'action',     'GMAIL_FETCH_EMAILS',
         'all_active', true,
         'sink',       'followups',
         'arguments',  jsonb_build_object(
            'query',       'in:inbox newer_than:2d',
            'max_results', 25,
            'verbose',     true
         )
       ),
       enabled = true
 WHERE name = 'inbox-triage';

COMMIT;
