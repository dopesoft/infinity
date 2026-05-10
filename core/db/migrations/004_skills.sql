-- 004_skills.sql — Skills system + Voyager skill state.
-- Provides: registry, version history, active pointer, runs.
-- Voyager extends mem_skill_versions with test_pass_rate / source / parent_version
-- so the schema lands once.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_skills (
    name            TEXT PRIMARY KEY,
    description     TEXT NOT NULL DEFAULT '',
    risk_level      TEXT NOT NULL DEFAULT 'low',
    network_egress  JSONB NOT NULL DEFAULT '[]'::jsonb,
    trigger_phrases JSONB NOT NULL DEFAULT '[]'::jsonb,
    inputs          JSONB NOT NULL DEFAULT '[]'::jsonb,
    outputs         JSONB NOT NULL DEFAULT '[]'::jsonb,
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active',  -- active | archived | candidate
    source          TEXT NOT NULL DEFAULT 'manual',  -- manual | openclaw_imported | hermes_imported | auto_evolved
    last_evolved    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_skills_status ON mem_skills(status);
CREATE INDEX IF NOT EXISTS idx_mem_skills_source ON mem_skills(source);

CREATE TABLE IF NOT EXISTS mem_skill_versions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    skill_name      TEXT NOT NULL REFERENCES mem_skills(name) ON DELETE CASCADE,
    version         TEXT NOT NULL,
    skill_md        TEXT NOT NULL,
    implementation  TEXT NOT NULL DEFAULT '',
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0,
    test_pass_rate  DOUBLE PRECISION NOT NULL DEFAULT 0,
    source          TEXT NOT NULL DEFAULT 'manual',
    parent_version  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    promoted_at     TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ,
    UNIQUE (skill_name, version)
);

CREATE INDEX IF NOT EXISTS idx_skill_versions_skill ON mem_skill_versions(skill_name);

CREATE TABLE IF NOT EXISTS mem_skill_active (
    skill_name      TEXT PRIMARY KEY REFERENCES mem_skills(name) ON DELETE CASCADE,
    active_version  TEXT NOT NULL,
    pinned          BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mem_skill_runs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    skill_name      TEXT NOT NULL,
    version         TEXT,
    session_id      UUID,
    trigger_source  TEXT NOT NULL DEFAULT 'manual',  -- manual | conversation | cron | heartbeat | sentinel
    input           JSONB NOT NULL DEFAULT '{}'::jsonb,
    output          TEXT NOT NULL DEFAULT '',
    success         BOOLEAN NOT NULL DEFAULT TRUE,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_skill_runs_name_started
    ON mem_skill_runs(skill_name, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_skill_runs_session ON mem_skill_runs(session_id);

COMMIT;
