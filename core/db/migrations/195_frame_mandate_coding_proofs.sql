-- 195_frame_mandate_coding_proofs.sql — teach frame-the-mandate the coding case.
--
-- Migration 194 made a Mandate able to GATE a plan step: bind one with
-- `step_id` and that step cannot be marked done until every criterion passes.
-- A gate nothing binds to is decoration, so this is the other half of the same
-- change: the judgment for WHEN to bind one and WHAT the criteria are for a
-- coding task.
--
-- The four proofs are the boss's own rule (2026-08-28): a coding task is done
-- when it builds and tests pass, the migrations are actually applied, the real
-- user path has been exercised, and it's committed with a clean tree. Every one
-- of those is a thing that has been claimed and been false.
--
-- Rule #1b holds. This is judgment only — which tasks deserve a mandate, how to
-- word the criteria for this repo, when to bind to a step. The MECHANICS are in
-- Go and cannot be dropped: the gate itself (mandate.CheckStepDone, wired at
-- plan_update, the one chokepoint every route to `done` flows through), and the
-- migrations proof, which is checked against schema_migrations in Go rather
-- than trusted to the model — that is the one claim with a track record, since
-- 011 through 014 sat unapplied in production for weeks while a prior session
-- asserted they were live.
--
-- Amends every version in place (the idiom from 134/135) so the Voyager-evolved
-- active version gets it too, not just the seed. Idempotent: guarded on the
-- section being absent.

BEGIN;

UPDATE mem_skill_versions
   SET skill_md = skill_md || $add$

## When the task is code

Two extra things apply, and only these two.

**Bind the mandate to the plan step.** Pass `step_id` (the step's number in the
plan is fine) to `mandate_open`. That makes this mandate the gate on that step:
it cannot go green until every criterion here passes. Without the binding the
mandate is a note beside the work; with it, it is the definition of done for
the thing the boss is actually watching.

**Use the four proofs as your criteria.** Shipped code is done when all four are
true, and each has been claimed falsely before:

1. It builds and the tests pass — name the exact commands for this repo, and
   what they must exit with.
2. Any new migration is APPLIED to the live database, not merely written.
3. The real path a person takes has actually been exercised — not just that the
   function compiles, but that the thing works from where someone uses it.
4. It is committed and the tree is clean, so nothing is stranded uncommitted.

Word them for the repo in front of you rather than pasting these back. Drop one
only when it genuinely does not apply — a docs-only change has no migration to
apply — and say so to the boss rather than quietly leaving it out.

If a coding job comes back STILL RUNNING or gets interrupted, that is not a
failure and not a finish. Pick it back up with `code_agent` and the run's
`resume_session` so it continues with its own context instead of redoing work
that is already on disk.
$add$
 WHERE skill_name = 'frame-the-mandate'
   AND skill_md NOT LIKE '%## When the task is code%';

COMMIT;
