-- 125_inbox_triage_concrete_gmail_verbs.sql — make the Gmail verbs directly
-- callable in the locked triage turn.
--
-- The tool_scope lockdown + pre-load (migrations 123/124, code 722db35) made
-- skills_invoke and the connector/surface tools directly callable by pre-loading
-- the CONCRETE allowlist entries as permanent. But the Gmail verbs were covered
-- only by the WILDCARD `composio__GMAIL_*`, which can't be pre-loaded — so the
-- agent had to load_tools them with a 1-turn TTL, and they decayed before it
-- could call them. Result (observed run 812a645d): discovered all 3 mailboxes,
-- loaded the Gmail verbs, but "they were not actually exposed as callable",
-- marked coverage=error, surfaced zero follow-ups.
--
-- Fix: add the specific read+draft Gmail verbs triage uses as CONCRETE entries
-- (alongside the wildcard, which stays for visibility of any other verb). Concrete
-- entries are pre-loaded permanent by ScopeConcreteAllow → callable from the first
-- iteration, no load dance, no decay. Never includes SEND/REPLY/DELETE/TRASH —
-- triage drafts only, never sends.
--
-- No code change: the pre-load logic already reads concrete allow entries. This
-- only widens the allowlist. Idempotent; guarded by name.

BEGIN;

-- Canonical allowlist for inbox-triage, shared by the cron's explicit scope and
-- the skill's declared tools.
WITH allow AS (
  SELECT jsonb_build_array(
    'skills_invoke',
    'load_tools', 'unload_tools', 'tool_search',
    'plan_*',
    'connector_accounts_list', 'connector_coverage_mark', 'connector_identity_set',
    'surface_item', 'surface_update', 'followup_list',
    'trust_batch_assign',
    'composio__GMAIL_*',
    'composio__GMAIL_FETCH_EMAILS',
    'composio__GMAIL_LIST_THREADS',
    'composio__GMAIL_FETCH_MESSAGE_BY_THREAD_ID',
    'composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID',
    'composio__GMAIL_LIST_DRAFTS',
    'composio__GMAIL_CREATE_EMAIL_DRAFT',
    'composio__GMAIL_GET_PROFILE'
  ) AS list
)
UPDATE mem_crons
   SET target_config = COALESCE(target_config, '{}'::jsonb)
       || jsonb_build_object('tool_scope', jsonb_build_object('allow', (SELECT list FROM allow)))
 WHERE name = 'inbox-triage';

UPDATE mem_skills
   SET allowed_tools = jsonb_build_array(
     'skills_invoke',
     'load_tools', 'unload_tools', 'tool_search',
     'plan_*',
     'connector_accounts_list', 'connector_coverage_mark', 'connector_identity_set',
     'surface_item', 'surface_update', 'followup_list',
     'trust_batch_assign',
     'composio__GMAIL_*',
     'composio__GMAIL_FETCH_EMAILS',
     'composio__GMAIL_LIST_THREADS',
     'composio__GMAIL_FETCH_MESSAGE_BY_THREAD_ID',
     'composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID',
     'composio__GMAIL_LIST_DRAFTS',
     'composio__GMAIL_CREATE_EMAIL_DRAFT',
     'composio__GMAIL_GET_PROFILE'
   )
 WHERE name = 'inbox-triage';

COMMIT;
