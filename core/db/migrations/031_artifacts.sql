-- 031_artifacts.sql — Jarvis's index of "things I've made."
--
-- Files on disk are one storage class. Cloudflare R2 / Supabase Storage
-- blobs are another. Postgres rows are a third. mem_artifacts is the
-- unified metadata layer: regardless of where the bytes live, the agent
-- thinks in terms of artifact rows.
--
-- Two key columns drive the boss-facing UX:
--
--   virtual_path  — agent-chosen organisation, NOT necessarily a real
--                   filesystem path. e.g. /projects/habit-tracker,
--                   /images/landing-page/hero-v3.png. Powers the
--                   Library tree view in Studio so the boss can browse
--                   "like a normal computer" without anything actually
--                   needing to be on disk.
--
--   storage_path  — where the bytes ACTUALLY live. For code projects
--                   this matches a real path on the active bridge. For
--                   generated media this is an object-store URL
--                   (https://<r2|supabase>/...).
--
-- Kinds (extensible — keep additions semantic, not technical):
--   project   — a directory tree, usually a git repo
--   image     — bitmap or vector
--   audio     — voice notes, generations, recordings
--   video     — clips, recordings
--   document  — long-form text (markdown, pdf, docx)
--   dataset   — csv, jsonl, parquet
--   memory    — promoted memories that also have blob form
--   other     — escape hatch

CREATE TABLE IF NOT EXISTS mem_artifacts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind              TEXT NOT NULL,
    name              TEXT NOT NULL,
    description       TEXT,
    -- Where bytes live.
    storage_kind      TEXT NOT NULL CHECK (storage_kind IN ('filesystem', 'object_store', 'postgres', 'inline')),
    storage_path      TEXT,                                    -- /workspace/projects/foo OR https://r2.../foo.png
    storage_size      BIGINT,                                  -- bytes; null when unknown / not applicable
    storage_mime      TEXT,                                    -- image/png, application/pdf, application/x-go etc.
    -- Where the BOSS sees it in the Library tree. Independent from
    -- storage_path so the agent can organise media however it wants.
    virtual_path      TEXT NOT NULL,
    -- Which bridge owns the storage (for filesystem kind) — mac | cloud
    -- | null. Lets Studio show "this project lives on your Mac" vs
    -- "this lives in the cloud workspace."
    bridge            TEXT CHECK (bridge IN ('mac', 'cloud') OR bridge IS NULL),
    -- Provenance.
    source_session_id UUID REFERENCES mem_sessions(id) ON DELETE SET NULL,
    source_tool       TEXT,                                    -- 'project_create' | 'image_generate' | …
    derived_from      UUID REFERENCES mem_artifacts(id) ON DELETE SET NULL,
    -- Code-specific shortcut: the GitHub repo URL when this artifact
    -- is a project that's been pushed to GitHub.
    github_url        TEXT,
    -- Free-form.
    tags              JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Lifecycle.
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accessed_at       TIMESTAMPTZ,                              -- updated when artifact_get hits the row
    deleted_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mem_artifacts_kind
    ON mem_artifacts(kind) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_virtual_path
    ON mem_artifacts(virtual_path) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_name_lower
    ON mem_artifacts(LOWER(name)) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_session
    ON mem_artifacts(source_session_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_tags
    ON mem_artifacts USING gin(tags) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_created
    ON mem_artifacts(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mem_artifacts_derived
    ON mem_artifacts(derived_from) WHERE deleted_at IS NULL;

-- Uniqueness: a virtual_path is the boss-facing identity. Two artifacts
-- shouldn't collide on it. We enforce via a partial-unique index so soft-
-- deleted rows don't block re-using a path.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mem_artifacts_virtual_path
    ON mem_artifacts(virtual_path) WHERE deleted_at IS NULL;

COMMENT ON TABLE  mem_artifacts IS 'Index of agent-created artifacts (projects, images, documents, …) regardless of storage class.';
COMMENT ON COLUMN mem_artifacts.virtual_path IS 'Boss-facing organisation path — folder tree in the Library UI. Not necessarily a real filesystem path.';
COMMENT ON COLUMN mem_artifacts.storage_path IS 'Where bytes actually live. Real FS path for code, object-store URL for media.';
