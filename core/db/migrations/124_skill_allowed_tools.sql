-- 124_skill_allowed_tools.sql — make a skill's tool surface a property of the
-- SKILL, so every cron that runs it inherits the lockdown deterministically.
--
-- The boss creates crons by asking the agent ("triage gmail every 4h"). He must
-- not have to remember — and must not have to tell the agent to remember — to
-- attach a tool_scope. That's the exact droppable-prose footgun we just removed
-- from the triage turn (Rule #1b). So scope lives on the skill: mem_skills.
-- allowed_tools declares the tools the skill needs; cron Upsert (scheduler.
-- deriveToolScope) auto-stamps target_config.tool_scope from it whenever a cron
-- runs that skill. No per-cron config, no agent judgment.
--
-- A skill with NULL/empty allowed_tools imposes no scope (full toolset), so
-- broad skills (self-improve, nightly-cognition) are unaffected — only skills
-- that declare a surface get locked. An explicit tool_scope on a cron still
-- wins. Trailing "*" entries are prefix wildcards (composio__GMAIL_*, plan_*).
--
-- Idempotent: ADD COLUMN IF NOT EXISTS + a guarded UPDATE by name.

BEGIN;

ALTER TABLE mem_skills ADD COLUMN IF NOT EXISTS allowed_tools jsonb;

COMMENT ON COLUMN mem_skills.allowed_tools IS
  'Optional tool allowlist (JSON array; trailing "*" = prefix wildcard). When set, any cron running this skill auto-derives target_config.tool_scope from it so the turn is locked to these tools. NULL/empty = full toolset.';

UPDATE mem_skills
   SET allowed_tools = jsonb_build_array(
         'skills_invoke',
         'load_tools', 'unload_tools', 'tool_search',
         'plan_*',
         'connector_accounts_list', 'connector_coverage_mark', 'connector_identity_set',
         'surface_item', 'surface_update', 'followup_list',
         'trust_batch_assign',
         'composio__GMAIL_*'
       )
 WHERE name = 'inbox-triage';

COMMIT;
