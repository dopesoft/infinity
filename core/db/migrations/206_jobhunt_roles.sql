-- 206_jobhunt_roles.sql
--
-- mem_jobhunt_roles: the application pipeline for the `job_hunt` pursuit
-- experience.
--
-- One row per role the boss is tracking, from the moment it is discovered
-- through to offer or dead. The pipeline is a kanban: `stage` is the column
-- the card sits in, and `stage_changed_at` is how long it has sat there.
--
-- Scoping follows the Psycho-Cybernetics experience (192_pursuit_experience):
-- every row hangs off a pursuit, and a pursuit only becomes a job hunt when
-- its mem_pursuits.experience says so. Ordinary pursuits are untouched.
--
-- Idempotency is deliberate throughout (IF NOT EXISTS, DROP POLICY IF EXISTS)
-- so a partially upgraded install can re-run this safely.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_jobhunt_roles (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The job hunt this role belongs to. Cascade because a pipeline has no
    -- meaning once its pursuit is gone.
    pursuit_id       UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    company          TEXT NOT NULL,
    role_title       TEXT NOT NULL,

    -- Where the posting was found. Constrained so a typo in a nightly sweep
    -- is a write error rather than a silent new source nothing renders.
    source           TEXT NOT NULL
                     CONSTRAINT chk_jobhunt_roles_source
                     CHECK (source IN ('linkedin', 'builtin', 'google_jobs', 'wellfound', 'yc')),

    url              TEXT,
    location         TEXT,

    -- Compensation. comp_min / comp_max carry the parsed band when the
    -- posting states one; comp_text keeps the posting's own wording so
    -- nothing is lost when the band cannot be parsed.
    comp_min         INT,
    comp_max         INT,
    comp_text        TEXT,

    -- When the employer posted it, versus when we first saw it. Both are
    -- needed: staleness is measured from posted_at, pipeline age from
    -- discovered_at.
    posted_at        TIMESTAMPTZ,
    discovered_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- How well the role fits, 0 to 100, with the reasoning that produced it
    -- so the score is never an unexplained number on a card.
    fit_score        INT,
    fit_reasoning    TEXT,

    -- How likely the posting is a ghost listing, 0 to 100. ghost_flags holds
    -- the individual signals (reposted repeatedly, no named hiring manager,
    -- open 180 days) as a JSON array so new signals need no migration.
    ghost_score      INT,
    ghost_flags      JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- The kanban column. stage_changed_at is stamped on every move so the
    -- board can show how long a card has been sitting.
    stage            TEXT NOT NULL DEFAULT 'discovered'
                     CONSTRAINT chk_jobhunt_roles_stage
                     CHECK (stage IN ('discovered', 'reviewed', 'tailoring', 'applied',
                                      'outreached', 'responded', 'interviewing', 'offer', 'dead')),
    stage_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    notes            TEXT,

    -- The source's own id for this posting. Paired with `source` it is what
    -- makes a repeat sweep update the existing row instead of duplicating
    -- it. Nullable for a role entered by hand, and Postgres allows repeated
    -- NULLs under a UNIQUE constraint, so hand-entered rows never collide.
    external_id      TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_jobhunt_roles_source_external UNIQUE (source, external_id)
);

-- Bounds on the two scores. Written as named constraints added out of line so
-- a re-run against an existing table still installs them.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_jobhunt_roles_fit_score') THEN
        ALTER TABLE mem_jobhunt_roles ADD CONSTRAINT chk_jobhunt_roles_fit_score
            CHECK (fit_score IS NULL OR fit_score BETWEEN 0 AND 100);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_jobhunt_roles_ghost_score') THEN
        ALTER TABLE mem_jobhunt_roles ADD CONSTRAINT chk_jobhunt_roles_ghost_score
            CHECK (ghost_score IS NULL OR ghost_score BETWEEN 0 AND 100);
    END IF;
END $$;

-- Every read is scoped to one pursuit, and the board reads a pursuit's rows
-- grouped by stage, so those are the two indexes that matter.
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_roles_pursuit
    ON mem_jobhunt_roles (pursuit_id);
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_roles_stage
    ON mem_jobhunt_roles (stage);

-- Realtime publication, so the board live updates as a sweep files new roles.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_jobhunt_roles'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_jobhunt_roles';
        END IF;
    END IF;
END $$;

-- RLS. Same read policy every realtime mem_* table carries (see
-- core/db/migrations/083_realtime_rls_read_policies.sql, 116_mem_plans.sql,
-- 164_artifacts_realtime_rls.sql): without a policy the authenticated
-- realtime socket is denied every row and Supabase silently drops all its
-- change events. Single-user safe: authenticated == the boss, read only, and
-- Core owns writes through pgx as the table owner, which bypasses RLS.
ALTER TABLE public.mem_jobhunt_roles ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS mem_jobhunt_roles_realtime_authenticated_read ON public.mem_jobhunt_roles;

CREATE POLICY mem_jobhunt_roles_realtime_authenticated_read
  ON public.mem_jobhunt_roles
  FOR SELECT TO authenticated
  USING (true);

COMMIT;
