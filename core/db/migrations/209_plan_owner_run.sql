-- 209: a plan can be OWNED by the run that authors it.
--
-- WHY
--
-- Claude Code on the Mac runs its OWN agent loop inside `claude -p`, with its
-- own TodoWrite checklist. Infinity read exactly two things out of that
-- stream: the newest tool call (a label like "Claude Code · Bash sed -n …")
-- and a count of distinct activities, which ProgressForSteps turned into a
-- percentage that means nothing (0.15 + 0.10 per activity, capped at 0.90).
-- So the strip above the composer — the SAME BackgroundJobDock that draws a
-- real plan for every other brain — fell through to its no-plan fallback and
-- showed the boss a raw shell command next to a fabricated 25%. His words:
-- "why does claude code have this same interaction to show a progress bar,
-- instead of translating its tasks into our plan UI?"
--
-- The nested checklist now syncs onto mem_plans through the same seam
-- todo_write uses (Store.SyncChecklist), so a Claude Code build shows the
-- identical checklist, count and step titles as any other model. One
-- substrate, one dock, one shape.
--
-- WHY A COLUMN AND NOT A CONVENTION
--
-- SyncChecklist REPLACES the active plan's step set. Without an owner, a
-- nested job would silently wipe a plan the boss's own brain laid out with
-- plan_create moments earlier and then delegated — deleting his checklist to
-- show its own. Ownership makes the guard a mechanic (Rule #1b) rather than a
-- sentence somebody has to remember: a nested job may only write the plan it
-- created itself, and declines otherwise.
--
-- NULL is the normal case: a plan authored by the boss, by plan_create, or by
-- the native todo_write tool is owned by the conversation, not by a run.
-- No FK to mem_runs: a run row can be reaped or purged long before the plan
-- it made, and losing the run must never cascade into losing the plan.

ALTER TABLE mem_plans
    ADD COLUMN IF NOT EXISTS owner_run_id UUID;

COMMENT ON COLUMN mem_plans.owner_run_id IS
    'The mem_runs row that authored this plan (a nested Claude Code job mirroring its own TodoWrite checklist). NULL when the conversation owns the plan, which is the normal case. Only the owning run may replace an owned plan''s steps.';

-- The lookup is always "the active plan for this session", which
-- 116_mem_plans already indexes; this one keeps the ownership check cheap when
-- reconciling a run's plans directly.
CREATE INDEX IF NOT EXISTS idx_mem_plans_owner_run
    ON mem_plans (owner_run_id)
    WHERE owner_run_id IS NOT NULL;
