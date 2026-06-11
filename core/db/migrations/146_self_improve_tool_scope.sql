-- 146_self_improve_tool_scope.sql — lock the self-improve crons to their real
-- toolset, and repair the hijacked nightly-self-improve skill version.
--
-- Two observed failures from the 2026-06-09 night runs:
--
-- 1. post-deploy-verify burned its single turn on plan bookkeeping and tool
--    housekeeping, then quit without ever calling deploy_status. Both
--    self-improve crons were seeded (087) with target_config '{}' — no
--    tool_scope — so the whole registry was visible and the model wandered.
--    Fix: the same explicit allowlist mechanism inbox-triage uses (125).
--    deriveToolScope can't auto-stamp these crons because their targets are
--    prose ("Run your `X` skill now"), not skills_invoke({...}) — so the scope
--    is set explicitly here AND mirrored onto mem_skills.allowed_tools so any
--    future cron that runs these skills inherits it (124's contract).
--
-- 2. skills_invoke returned "unknown skill: nightly-self-improve" even though
--    mem_skills.status='active'. Root cause: on 2026-06-08 Voyager promoted
--    versions v1.1-6-8-2026 / v1.2-6-8-2026 whose skill_md is a DIFFERENT
--    skill ("recover_and_finalize_incomplete_self_improvement_run") onto
--    nightly-self-improve's active pointer. Materialize wrote that body to
--    skills/nightly_self_improve/SKILL.md, the loader keyed the registry by
--    the body's frontmatter name, and "nightly-self-improve" vanished from
--    the registry. Fix here: repoint the active version to the last good body
--    (v1.0-6-2-2026, the manual recipe) and archive the two foreign-body
--    versions. The code-side guard that prevents recurrence lives in
--    voyager.persistSkillToDB (frontmatter-name routing).
--
-- Idempotent: guarded by name/version; jsonb merge preserves other
-- target_config keys; archive only stamps NULL archived_at.

BEGIN;

-- ── 1a. nightly-self-improve: everything the v1.0-6-2-2026 recipe names,
--        on both bridges, plus plan bookkeeping (a multi-item night earns a
--        step timeline; abandoned steps are honest signal post-3c).
WITH allow AS (
  SELECT jsonb_build_array(
    'skills_invoke',
    'load_tools', 'unload_tools', 'tool_search',
    'plan_*',
    'mem_list',
    'deploy_status', 'self_improve_control',
    'code_proposal_decide', 'question_list', 'question_decide',
    'fs_*', 'bash_run', 'git_*',
    'code_agent', 'claude_code__*',
    'github__*',
    'surface_item', 'surface_update',
    'recall', 'remember'
  ) AS list
)
UPDATE mem_crons
   SET target_config = COALESCE(target_config, '{}'::jsonb)
       || jsonb_build_object('tool_scope', jsonb_build_object('allow', (SELECT list FROM allow)))
 WHERE name = 'nightly-self-improve';

UPDATE mem_skills
   SET allowed_tools = jsonb_build_array(
     'skills_invoke',
     'load_tools', 'unload_tools', 'tool_search',
     'plan_*',
     'mem_list',
     'deploy_status', 'self_improve_control',
     'code_proposal_decide', 'question_list', 'question_decide',
     'fs_*', 'bash_run', 'git_*',
     'code_agent', 'claude_code__*',
     'github__*',
     'surface_item', 'surface_update',
     'recall', 'remember'
   )
 WHERE name = 'nightly-self-improve';

-- ── 1b. post-deploy-verify: deliberately narrower. Its turn is one health
--        check plus at most one revert. NO plan_* — this is the cron that
--        burned its turn on plan bookkeeping; with plan tools invisible it
--        physically cannot repeat that.
WITH allow AS (
  SELECT jsonb_build_array(
    'skills_invoke',
    'load_tools', 'unload_tools', 'tool_search',
    'deploy_status',
    'fs_read', 'fs_ls', 'bash_run', 'git_*',
    'code_proposal_decide',
    'surface_item', 'surface_update',
    'recall', 'remember'
  ) AS list
)
UPDATE mem_crons
   SET target_config = COALESCE(target_config, '{}'::jsonb)
       || jsonb_build_object('tool_scope', jsonb_build_object('allow', (SELECT list FROM allow)))
 WHERE name = 'post-deploy-verify';

UPDATE mem_skills
   SET allowed_tools = jsonb_build_array(
     'skills_invoke',
     'load_tools', 'unload_tools', 'tool_search',
     'deploy_status',
     'fs_read', 'fs_ls', 'bash_run', 'git_*',
     'code_proposal_decide',
     'surface_item', 'surface_update',
     'recall', 'remember'
   )
 WHERE name = 'post-deploy-verify';

-- ── 2. Repair the hijacked active pointer. Only acts when the pointer is
--       still on one of the two foreign-body versions AND the known-good
--       manual recipe exists — a later legitimate repoint is left alone.
UPDATE mem_skill_active a
   SET active_version = 'v1.0-6-2-2026',
       updated_at     = NOW()
 WHERE a.skill_name = 'nightly-self-improve'
   AND a.active_version IN ('v1.1-6-8-2026', 'v1.2-6-8-2026')
   AND EXISTS (
     SELECT 1 FROM mem_skill_versions v
      WHERE v.skill_name = 'nightly-self-improve'
        AND v.version = 'v1.0-6-2-2026'
   );

-- Soft-archive the foreign bodies so history keeps the trail but nothing can
-- repoint to them by accident. No deletes.
UPDATE mem_skill_versions
   SET archived_at = NOW()
 WHERE skill_name = 'nightly-self-improve'
   AND version IN ('v1.1-6-8-2026', 'v1.2-6-8-2026')
   AND archived_at IS NULL;

COMMIT;
