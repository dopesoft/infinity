-- 121_plan_close_on_abandon.sql
--
-- Pairs with the recompute() change (a failed step now pauses the plan instead
-- of leaving it silently 'active'). That's the MECHANIC. This is the JUDGMENT
-- (Rule #1b): the plan-and-verify skill (117) told Jarvis how to finish or
-- partially-finish a plan, but never what to do when he ABANDONS one. So a plan
-- with a failed step and dangling pending steps lingered forever, injected into
-- every turn and sitting on the boss's dashboard. (2026-06-03: the Higgsfield
-- plan stopped at "install" failed, steps 3-4 never closed, stuck at 25%.)
--
-- Adds section 7 + a hard rule: when you stop pursuing a plan, close it out
-- (mark remaining steps skipped / the failed one failed, or supersede with a new
-- plan) so it reaches a terminal state and drops out of context.
--
-- Idempotent: the NOT LIKE guard makes a re-run a no-op.

BEGIN;

UPDATE mem_skill_versions
SET skill_md = replace(
  skill_md,
  $anchor$## Hard rules$anchor$,
  $new$## 7. Closing a plan you are walking away from

If you decide to STOP pursuing a plan - a step failed and you are not retrying, or
priorities changed and the rest no longer matters - do NOT just leave it. Close it
out so it reaches a terminal state and stops being injected into every future turn:
mark each remaining pending step `skipped` (or the one that failed `failed`) with
plan_update, or start a fresh `plan_create` which supersedes it. A failed step
pauses the whole plan, so it sits flagged as needs-attention on the boss's
dashboard until you resolve it. A half-open abandoned plan clutters your context
every turn and misrepresents what is still in flight - never leave one hanging.

## Hard rules$new$
)
WHERE skill_name = 'plan-and-verify'
  AND version = '1.0.0'
  AND skill_md NOT LIKE '%Closing a plan you are walking away from%';

COMMIT;
