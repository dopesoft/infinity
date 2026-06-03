-- 127_inbox_triage_llm_decides.sql — point inbox-triage at the deterministic
-- skill that lets the LLM decide.
--
-- The connector_poll version (126) fetched and DUMPED everything to the
-- dashboard — ads, newsletters, the lot — and never let the model judge what
-- the boss should actually see. The boss: "give the LLM the chance to decide
-- what it thinks I should get back."
--
-- The new path (core/internal/inbox) is a deterministic skill run as a
-- system_task: fetch broad across all active Gmail accounts (code) → ONE batched
-- call to the boss's Settings model that picks which emails need his reply (the
-- judgment) → surface the winners through the surface contract so they render
-- with Draft/Archive/Snooze + full HTML body, exactly like the working version.
-- No agent loop, no tool-loading, one LLM call. This is the reference shape for
-- every future cron.
--
-- query stays BROAD on purpose — the LLM is the filter, not a Gmail category
-- prefilter. Requires the matching code deployed; the scheduler reloads at boot.
-- Idempotent; guarded by name.

BEGIN;

UPDATE mem_crons
   SET job_kind     = 'system_task',
       target       = 'inbox_triage',
       target_config = jsonb_build_object(
         'task',        'inbox_triage',
         'query',       'in:inbox newer_than:3d',
         'max_results', 40
       ),
       enabled = true
 WHERE name = 'inbox-triage';

COMMIT;
