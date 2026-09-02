-- 210: a job posting's uniqueness is scoped to the PURSUIT that swept it.
--
-- WHY
--
-- 206_jobhunt_roles declared:
--
--     CONSTRAINT uq_jobhunt_roles_source_external UNIQUE (source, external_id)
--
-- which is global, not per pursuit. Every read in core/internal/pursuits/jh is
-- scoped to one pursuit, and the table hangs off mem_pursuits precisely so two
-- job hunts stay separate — but this one constraint was not scoped, so the
-- moment a SECOND job_hunt pursuit swept a posting the first had already
-- filed, `Store.UpsertRole` (which conflicts on this constraint) would take
-- the DO UPDATE branch against the FIRST pursuit's row. Its pursuit_id is not
-- in the SET list, so the row stays where it was, and RETURNING hands the
-- caller a Role belonging to somebody else's pipeline. The sweeping pursuit
-- never gets its own card, the other pursuit's card is silently overwritten
-- with a stranger's fit score and notes, and nothing anywhere errors. A
-- cross-pursuit data leak that reads exactly like a successful upsert.
--
-- The fix is the scoping the rest of the table already assumes: the same
-- posting may appear once PER PURSUIT, and a repeat sweep by that pursuit
-- still updates its own row rather than duplicating it.
--
-- NULL SEMANTICS ARE UNCHANGED
--
-- Still a plain (non-partial) UNIQUE constraint, so Postgres keeps treating
-- NULLs as distinct: a role entered by hand has external_id NULL (UpsertRole
-- writes NULLIF($17, '')) and therefore never collides with anything,
-- including another hand-entered role in the same pursuit. That was 206's
-- deliberate behaviour and it survives verbatim — only the pursuit_id column
-- is added to the key.
--
-- SAFE TO RE-RUN, AND SAFE ON EXISTING DATA
--
-- The new key is strictly weaker than the old one: any pair of rows that
-- would violate (pursuit_id, source, external_id) already violates
-- (source, external_id) and so cannot exist today. Dropping the tight
-- constraint before adding the loose one therefore cannot fail on live rows,
-- and both halves are guarded so a partially upgraded install re-runs clean.

ALTER TABLE mem_jobhunt_roles
    DROP CONSTRAINT IF EXISTS uq_jobhunt_roles_source_external;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'uq_jobhunt_roles_pursuit_source_external'
           AND conrelid = 'public.mem_jobhunt_roles'::regclass
    ) THEN
        ALTER TABLE mem_jobhunt_roles
            ADD CONSTRAINT uq_jobhunt_roles_pursuit_source_external
            UNIQUE (pursuit_id, source, external_id);
    END IF;
END $$;

COMMENT ON CONSTRAINT uq_jobhunt_roles_pursuit_source_external ON mem_jobhunt_roles IS
    'One row per (pursuit, source, external_id): a repeat sweep updates the pursuit''s own card, and two pursuits tracking the same posting each keep their own. NULL external_id (a role entered by hand) never collides.';
