-- 208_jobhunt_experience.sql
--
-- Widen the mem_pursuits.experience whitelist to admit 'job_hunt'.
--
-- 192_pursuit_experience.sql pinned experience to ('ordinary',
-- 'psycho_cybernetics'). Every new bespoke experience has to be added here
-- first, otherwise the insert is rejected by chk_mem_pursuits_experience and
-- the cockpit never gets a row to render.
--
-- 'job_hunt' backs the remote Head/VP Product search: a kanban pipeline of
-- roles (mem_jobhunt_roles), an interview answer corpus, outreach contacts,
-- and per-role artifacts (all in 206/207).
--
-- Drop-then-add rather than a bare ADD, because the constraint already exists
-- from 192. Guarded so a re-run against an already-widened install is a no-op.

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_mem_pursuits_experience') THEN
        ALTER TABLE mem_pursuits DROP CONSTRAINT chk_mem_pursuits_experience;
    END IF;

    ALTER TABLE mem_pursuits ADD CONSTRAINT chk_mem_pursuits_experience
        CHECK (experience IN ('ordinary', 'psycho_cybernetics', 'job_hunt'));
END $$;

COMMIT;
