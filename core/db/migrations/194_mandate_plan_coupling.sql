-- 194_mandate_plan_coupling.sql — couple a Mandate to the plan step it defines
-- "done" for, so the plan's done-gate is the mandate's done-gate.
--
-- WHY. Two substrates already answer "is this finished?", and they did not
-- talk to each other:
--
--   mem_mandates  a real contract. Binary criteria, evidence per criterion, and
--                 a done-gate enforced in Go (mandate.Store.Close) that refuses
--                 to close while anything is unproven.
--   mem_plan_steps  what Jarvis actually marks done as he works — and the only
--                 gate on `done` was verify_required, which most steps do not
--                 set. So the step the boss watches could go green on the
--                 model's word while the contract beside it sat unproven.
--
-- Coupling them means the gate that already exists starts guarding the thing
-- the boss is actually looking at. Nothing new is invented: mandate.Close's
-- criteria check becomes the plan step's check too, one implementation.
--
-- This is the substrate for the boss's rule (2026-08-28) that a coding task is
-- done only with all four proofs: build and tests pass, migrations applied, the
-- real user path exercised, committed with a clean tree. Those become criteria
-- on a mandate bound to the step, and the step cannot go done until each one is
-- ticked WITH evidence.
--
-- Both columns are nullable and unconstrained by foreign keys on purpose: a
-- mandate for work with no plan is still perfectly valid (a cron's mandate, a
-- one-shot task), and a plan deleted out from under a historical mandate must
-- not cascade into losing the record of what was promised.

BEGIN;

ALTER TABLE mem_mandates ADD COLUMN IF NOT EXISTS plan_id UUID;
ALTER TABLE mem_mandates ADD COLUMN IF NOT EXISTS step_id UUID;

-- The hot query is the gate's: "does an open mandate guard THIS step?", run on
-- every plan_update(done). Partial so it stays small — closed and abandoned
-- mandates never gate anything.
CREATE INDEX IF NOT EXISTS idx_mem_mandates_step_open
    ON mem_mandates (step_id)
    WHERE step_id IS NOT NULL AND status IN ('open','verifying');

-- Secondary: "what is promised for this plan?", for the plan detail surface.
CREATE INDEX IF NOT EXISTS idx_mem_mandates_plan
    ON mem_mandates (plan_id, updated_at DESC)
    WHERE plan_id IS NOT NULL;

COMMIT;
