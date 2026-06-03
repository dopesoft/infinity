-- 122_plan_revise_and_cancel.sql
--
-- Two new plan MECHANICS shipped in this change (core/internal/tools/plan_tools.go
-- + plan/store.go): `plan_revise` (edit/prune/add steps in place when work
-- diverts) and `plan_cancel` (kill a plan outright). This migration is the
-- matching JUDGMENT (Rule #1b): teach the plan-and-verify skill when to reach for
-- each, and the honesty rule that prevents the 2026-06-03 failure where a FAILED
-- Higgsfield install was marked 'done' (advancing the plan to 50%) because the
-- agent had no clean way to kill or re-shape the plan.
--
-- Idempotent: the NOT LIKE guard makes a re-run a no-op.

BEGIN;

UPDATE mem_skill_versions
SET skill_md = replace(
  skill_md,
  $anchor$## Hard rules$anchor$,
  $new$## 8. Diverting, or killing the plan (use the right tool, never fake it)

Plans change. When they do, reach for the tool that tells the truth:

- **The work diverted but the plan lives on** — adapt it with `plan_revise`: prune
  steps that no longer apply, rewrite a step to what you're actually doing now (e.g.
  step 2 was "Install X", it failed, so repurpose it to "Clean up the failed
  install"), or add new steps. Steps already finished stay finished. This is the
  right move — NOT ticking the dead step done and pushing on.
- **The boss says drop / kill / cancel / stop the plan, or it genuinely can't
  succeed** — call `plan_cancel`. It cancels the whole plan in one step (kept for
  history) and clears it off the dashboard. That is what "kill it" means; never
  approximate a kill by marking steps off.
- **"Done" means it actually worked.** A step that failed, or that you couldn't
  complete, is `failed` or `skipped` — NEVER `done`. A false completion turns the
  whole plan into a lie about what was accomplished.

## Hard rules
- "Done" requires it actually succeeded. Failed/abandoned steps are `failed` or
  `skipped`; "kill the plan" is `plan_cancel`; a divert is `plan_revise`. Never fake
  forward progress.$new$
)
WHERE skill_name = 'plan-and-verify'
  AND version = '1.0.0'
  AND skill_md NOT LIKE '%Diverting, or killing the plan%';

COMMIT;
