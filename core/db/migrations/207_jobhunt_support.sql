-- 207_jobhunt_support.sql
--
-- The three tables that sit around the pipeline in 206_jobhunt_roles.sql and
-- complete the `job_hunt` pursuit experience:
--
--   mem_jobhunt_corpus     the banked interview material. One row per
--                          question and the answer the boss actually gave,
--                          filed under a theme so a tailoring pass can pull
--                          the right story instead of inventing one.
--   mem_jobhunt_contacts   the humans. Hiring managers and recruiters, each
--                          optionally tied to the role they hire for, with
--                          where the outreach has got to.
--   mem_jobhunt_artifacts  what was generated per role: the tailored resume,
--                          the cover letter, the positioning read, and
--                          whether it is still a draft or has gone out.
--
-- Scoping follows 206 and the Psycho-Cybernetics experience
-- (192_pursuit_experience): every row hangs off a pursuit, and a pursuit only
-- becomes a job hunt when its mem_pursuits.experience says so. Ordinary
-- pursuits are untouched.
--
-- Idempotency is deliberate throughout (IF NOT EXISTS, DROP POLICY IF EXISTS)
-- so a partially upgraded install can re-run this safely.

BEGIN;

-- mem_jobhunt_corpus ------------------------------------------------------
-- The corpus is grouped by theme in the cockpit ("execution under pressure",
-- "ambiguity", "ai-native shipping"), so theme is a plain text column rather
-- than a constrained enum: the boss's themes emerge from his own material and
-- must not need a migration to add one.
CREATE TABLE IF NOT EXISTS mem_jobhunt_corpus (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    pursuit_id   UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    theme        TEXT NOT NULL,
    question     TEXT NOT NULL,
    answer       TEXT NOT NULL,

    -- The numbers inside the story, pulled out so a tailoring pass can quote
    -- them without re-parsing the prose. JSONB so a new metric shape needs no
    -- migration.
    metrics      JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags         TEXT[] NOT NULL DEFAULT '{}',

    -- Whether this came out of a sit-down interview session with the coach,
    -- or was dropped in on its own.
    source       TEXT NOT NULL
                 CONSTRAINT chk_jobhunt_corpus_source
                 CHECK (source IN ('interview', 'adhoc')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Every read is scoped to one pursuit, and the corpus renders grouped by
-- theme with counts, so those are the two indexes that matter.
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_corpus_pursuit
    ON mem_jobhunt_corpus (pursuit_id);
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_corpus_theme
    ON mem_jobhunt_corpus (theme);

-- mem_jobhunt_contacts ----------------------------------------------------
-- role_id is nullable and clears rather than cascades: a person the boss has
-- met is worth keeping after the role they were attached to dies, and losing
-- the contact along with the posting would quietly destroy the more valuable
-- half of the record.
CREATE TABLE IF NOT EXISTS mem_jobhunt_contacts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    pursuit_id       UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,
    role_id          UUID REFERENCES mem_jobhunt_roles(id) ON DELETE SET NULL,

    name             TEXT NOT NULL,
    title            TEXT,
    -- Denormalised from the role on purpose: a contact with no role_id still
    -- has to say who they work for.
    company          TEXT,
    linkedin_url     TEXT,
    email            TEXT,

    -- How far the outreach has got. 'sent' with nothing back is what the
    -- cockpit reads as waiting on a reply, which is why outreach_sent_at is
    -- stored rather than derived from the message text.
    outreach_status  TEXT NOT NULL DEFAULT 'identified'
                     CONSTRAINT chk_jobhunt_contacts_outreach_status
                     CHECK (outreach_status IN ('identified', 'drafted', 'sent', 'replied', 'dead')),
    outreach_sent_at TIMESTAMPTZ,
    last_message     TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_contacts_pursuit
    ON mem_jobhunt_contacts (pursuit_id);
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_contacts_outreach_status
    ON mem_jobhunt_contacts (outreach_status);

-- mem_jobhunt_artifacts ---------------------------------------------------
-- The generated documents, grouped by role in the cockpit. role_id cascades
-- because a tailored resume for a deleted role has nothing left to describe.
--
-- artifact_id points at the existing mem_artifacts table (031_artifacts.sql),
-- which is where the bytes and the virtual path live. It is nullable and
-- clears on delete because the row is filed the moment a document is
-- commissioned, which is before there is anything to point at.
CREATE TABLE IF NOT EXISTS mem_jobhunt_artifacts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    pursuit_id   UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,
    role_id      UUID NOT NULL REFERENCES mem_jobhunt_roles(id) ON DELETE CASCADE,

    kind         TEXT NOT NULL
                 CONSTRAINT chk_jobhunt_artifacts_kind
                 CHECK (kind IN ('resume', 'cover_letter', 'positioning_read')),

    artifact_id  UUID REFERENCES mem_artifacts(id) ON DELETE SET NULL,

    title        TEXT NOT NULL,

    status       TEXT NOT NULL DEFAULT 'draft'
                 CONSTRAINT chk_jobhunt_artifacts_status
                 CHECK (status IN ('draft', 'approved', 'sent')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_artifacts_pursuit
    ON mem_jobhunt_artifacts (pursuit_id);
CREATE INDEX IF NOT EXISTS idx_mem_jobhunt_artifacts_role
    ON mem_jobhunt_artifacts (role_id);

-- Realtime publication, so the cockpit live updates as a sweep banks corpus
-- entries, files contacts, or finishes a document.
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_jobhunt_corpus',
            'mem_jobhunt_contacts',
            'mem_jobhunt_artifacts'
        ]
        LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_publication_tables
                 WHERE pubname = 'supabase_realtime'
                   AND schemaname = 'public'
                   AND tablename = tname
            ) THEN
                EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tname);
            END IF;
        END LOOP;
    END IF;
END $$;

-- RLS. Same read policy every realtime mem_* table carries (see
-- core/db/migrations/083_realtime_rls_read_policies.sql, 116_mem_plans.sql,
-- 164_artifacts_realtime_rls.sql, and 206_jobhunt_roles.sql): without a
-- policy the authenticated realtime socket is denied every row and Supabase
-- silently drops all its change events. Single-user safe: authenticated ==
-- the boss, read only, and Core owns writes through pgx as the table owner,
-- which bypasses RLS.
DO $$
DECLARE
    tname TEXT;
    pol_name TEXT;
BEGIN
    FOREACH tname IN ARRAY ARRAY[
        'mem_jobhunt_corpus',
        'mem_jobhunt_contacts',
        'mem_jobhunt_artifacts'
    ]
    LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', tname);
        pol_name := tname || '_realtime_authenticated_read';
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', pol_name, tname);
        EXECUTE format(
            'CREATE POLICY %I ON public.%I FOR SELECT TO authenticated USING (true)',
            pol_name, tname
        );
    END LOOP;
END $$;

COMMIT;
