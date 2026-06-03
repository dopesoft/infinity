-- 123_inbox_triage_tool_scope.sql — lock the inbox-triage cron's turn to a
-- mail-only tool surface.
--
-- The repeated triage failure was never "Composio is revoked" — every live
-- mailbox authenticates fine. It was the runtime brain (gpt-5.x on the ChatGPT
-- OAuth path) wandering off-task: both observed stall runs reached the inbox,
-- then burned the turn in delegate (which 400s on that account), skill_optimize
-- loops, recall, system_map, and a hallucinated mem_list table — and quit with
-- the emails un-surfaced. delegate / system_map / recall / mem_list /
-- compact_context are CORE-PINNED, so they're in the brain's hand every turn.
--
-- Per Rule #1b, a prose "don't call those" is droppable (gpt-5.x has ignored
-- exactly that here). The mechanic instead: the cron carries a tool_scope.allow
-- list; the executor (tools.RegisterToolScopeForSession) + the loop's tool
-- visibility filter make every tool NOT on the list physically invisible for
-- the turn — overriding core-pinning. The brain can only do the mail job.
--
-- Generic: ANY cron can carry its own tool_scope in target_config; this only
-- seeds triage's. Idempotent — re-running re-sets the same scope. Guarded by
-- name so it can only touch the live inbox-triage cron.
--
-- Wildcards: trailing "*" is a prefix match (composio__GMAIL_* covers every
-- Gmail verb; plan_* covers create/update/verify/revise/cancel) so the scope
-- survives tool-name drift without a redeploy.

BEGIN;

UPDATE mem_crons
   SET target_config = COALESCE(target_config, '{}'::jsonb) || jsonb_build_object(
         'tool_scope', jsonb_build_object(
           'allow', jsonb_build_array(
             'skills_invoke',
             'load_tools', 'unload_tools', 'tool_search',
             'plan_*',
             'connector_accounts_list', 'connector_coverage_mark', 'connector_identity_set',
             'surface_item', 'surface_update', 'followup_list',
             'trust_batch_assign',
             'composio__GMAIL_*'
           )
         )
       )
 WHERE name = 'inbox-triage';

COMMIT;
