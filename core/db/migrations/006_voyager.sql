-- 006_voyager.sql — Voyager + Cron + Sentinels.
--
-- Adds:
--   • mem_crons        — scheduled jobs (cron expressions)
--   • mem_sentinels    — event-driven watchers
--   • mem_skill_proposals — curriculum-generated skill candidates
--   • mem_skill_failures  — production failures fed to AutoSkill
--   • mem_skill_tests     — synthetic test cases per skill

BEGIN;

CREATE TABLE IF NOT EXISTS mem_crons (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name              TEXT NOT NULL UNIQUE,
    schedule          TEXT NOT NULL,                -- cron expression, e.g. "0 9 * * 1-5"
    schedule_natural  TEXT,                          -- human-readable form
    job_kind          TEXT NOT NULL DEFAULT 'isolated_agent_turn'
        CHECK (job_kind IN ('system_event', 'isolated_agent_turn')),
    target            TEXT NOT NULL DEFAULT '',     -- prompt or instructions
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    max_retries       INT NOT NULL DEFAULT 3,
    backoff_seconds   INT NOT NULL DEFAULT 60,
    last_run_at       TIMESTAMPTZ,
    last_run_status   TEXT,
    last_run_duration_ms INT,
    next_run_at       TIMESTAMPTZ,
    failure_count     INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_crons_enabled ON mem_crons(enabled, next_run_at);

CREATE TABLE IF NOT EXISTS mem_sentinels (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name              TEXT NOT NULL UNIQUE,
    watch_type        TEXT NOT NULL
        CHECK (watch_type IN ('webhook','file_change','memory_event','external_api_poll','threshold')),
    watch_config      JSONB NOT NULL DEFAULT '{}'::jsonb,
    action_chain      JSONB NOT NULL DEFAULT '[]'::jsonb,
    cooldown_seconds  INT NOT NULL DEFAULT 300,
    last_triggered_at TIMESTAMPTZ,
    fire_count        INT NOT NULL DEFAULT 0,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sentinels_enabled ON mem_sentinels(enabled);

CREATE TABLE IF NOT EXISTS mem_skill_proposals (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name          TEXT NOT NULL,
    description   TEXT NOT NULL,
    reasoning     TEXT NOT NULL DEFAULT '',
    skill_md      TEXT NOT NULL DEFAULT '',
    risk_level    TEXT NOT NULL DEFAULT 'low',
    network_egress JSONB NOT NULL DEFAULT '[]'::jsonb,
    inputs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    outputs       JSONB NOT NULL DEFAULT '[]'::jsonb,
    test_pass_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'candidate', -- candidate | promoted | rejected
    parent_skill  TEXT,                               -- if patching an existing skill
    parent_version TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS mem_skill_failures (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    skill_name    TEXT NOT NULL,
    skill_version TEXT,
    run_id        UUID,
    failure_text  TEXT NOT NULL,
    patch_attempts INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'open',  -- open | patched | flagged
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_skill_failures_open
    ON mem_skill_failures(status, skill_name);

CREATE TABLE IF NOT EXISTS mem_skill_tests (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    skill_name    TEXT NOT NULL,
    description   TEXT NOT NULL,
    inputs        JSONB NOT NULL DEFAULT '{}'::jsonb,
    expected      TEXT NOT NULL DEFAULT '',
    last_run_at   TIMESTAMPTZ,
    last_passed   BOOLEAN,
    source        TEXT NOT NULL DEFAULT 'manual', -- manual | synthetic
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_skill_tests_skill ON mem_skill_tests(skill_name);

COMMIT;
