-- 137_plan_step_bg_run.sql - one-shot recovery guard on plan steps.
--
-- Background builds drive the boss's ONE durable plan: todo_write resolves the
-- parent chat session and writes its checklist into mem_plans/mem_plan_steps
-- (see tools/todo_tools.go - "a todo checklist and a plan are the SAME thing").
-- The fix this migration supports is the deterministic settle-on-completion:
-- when a detached background.build finishes, serve.go's OnDone settles the parent
-- session's active plan in code (success -> steps done + plan completed; failure
-- -> the in_progress step failed with the REAL error + plan paused). That mechanic
-- never depends on the LLM remembering to call plan_update (Rule #1b).
--
-- recovery_attempted is the guard that makes the boss's "one auto-recovery, then
-- stop" policy un-loopable: a transient/bridge-class background failure may be
-- re-dispatched exactly once; the flag flips true so a second failure stops and
-- surfaces the error instead of retrying forever.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS.

BEGIN;

ALTER TABLE mem_plan_steps ADD COLUMN IF NOT EXISTS recovery_attempted BOOLEAN NOT NULL DEFAULT FALSE;

COMMIT;
