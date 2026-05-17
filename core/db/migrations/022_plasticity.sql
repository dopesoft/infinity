-- 022_plasticity.sql - Gym / plasticity substrate.
--
-- Infinity already captures experience: memories, skills, trajectories,
-- evals, reflections, predictions, and audit rows. Gym is the improvement
-- ledger that turns those artifacts into measurable policy candidates.
--
-- This migration deliberately stores metadata and provenance, not model
-- blobs. Large datasets / adapters live in object storage or a sidecar
-- artifact store; Postgres keeps hashes, statuses, scores, and lineage.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_training_examples (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_kind   TEXT NOT NULL DEFAULT '',       -- session | memory | eval | reflection | manual
    source_id     TEXT NOT NULL DEFAULT '',
    task_kind     TEXT NOT NULL DEFAULT '',       -- intent | plan | preference | tool_policy | summary
    input_text    TEXT NOT NULL DEFAULT '',
    output_text   TEXT NOT NULL DEFAULT '',
    label         TEXT NOT NULL DEFAULT 'accepted', -- accepted | rejected | corrected | synthetic
    score         NUMERIC NOT NULL DEFAULT 0,
    privacy_class TEXT NOT NULL DEFAULT 'private',  -- private | redacted | shareable
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_training_examples_task_recent
    ON mem_training_examples (task_kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_training_examples_source
    ON mem_training_examples (source_kind, source_id);

CREATE TABLE IF NOT EXISTS mem_distillation_datasets (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'draft',  -- draft | ready | archived | failed
    example_count INT NOT NULL DEFAULT 0,
    filters       JSONB NOT NULL DEFAULT '{}'::jsonb,
    artifact_uri  TEXT NOT NULL DEFAULT '',
    checksum      TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_distillation_datasets_status_recent
    ON mem_distillation_datasets (status, updated_at DESC);

CREATE TABLE IF NOT EXISTS mem_model_adapters (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name           TEXT NOT NULL,
    base_model     TEXT NOT NULL DEFAULT '',
    adapter_uri    TEXT NOT NULL DEFAULT '',
    checksum       TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'candidate',
    task_scope     TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    metrics        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    promoted_at    TIMESTAMPTZ,
    rolled_back_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mem_model_adapters_status_recent
    ON mem_model_adapters (status, created_at DESC);

CREATE TABLE IF NOT EXISTS mem_distillation_runs (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dataset_id   UUID REFERENCES mem_distillation_datasets(id) ON DELETE SET NULL,
    adapter_id   UUID REFERENCES mem_model_adapters(id) ON DELETE SET NULL,
    status       TEXT NOT NULL DEFAULT 'queued', -- queued | training | evaluating | candidate | failed | promoted
    trigger      TEXT NOT NULL DEFAULT 'manual', -- manual | nightly | regression | curiosity
    reason       TEXT NOT NULL DEFAULT '',
    base_model   TEXT NOT NULL DEFAULT '',
    metrics      JSONB NOT NULL DEFAULT '{}'::jsonb,
    error        TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_distillation_runs_status_recent
    ON mem_distillation_runs (status, created_at DESC);

CREATE TABLE IF NOT EXISTS mem_adapter_evals (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    adapter_id       UUID REFERENCES mem_model_adapters(id) ON DELETE CASCADE,
    eval_name        TEXT NOT NULL,
    baseline_score   NUMERIC NOT NULL DEFAULT 0,
    candidate_score  NUMERIC NOT NULL DEFAULT 0,
    regression_count INT NOT NULL DEFAULT 0,
    passed           BOOLEAN NOT NULL DEFAULT FALSE,
    metrics          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_adapter_evals_adapter_recent
    ON mem_adapter_evals (adapter_id, created_at DESC);

CREATE TABLE IF NOT EXISTS mem_policy_routes (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    route             TEXT NOT NULL UNIQUE,       -- intent | plan_sketch | skill_suggestion | preference
    task_kind         TEXT NOT NULL DEFAULT '',
    active_adapter_id UUID REFERENCES mem_model_adapters(id) ON DELETE SET NULL,
    status            TEXT NOT NULL DEFAULT 'disabled', -- disabled | shadow | active | rolled_back
    confidence        NUMERIC NOT NULL DEFAULT 0,
    min_score         NUMERIC NOT NULL DEFAULT 0,
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_policy_routes_status
    ON mem_policy_routes (status, updated_at DESC);

DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_training_examples',
            'mem_distillation_datasets',
            'mem_model_adapters',
            'mem_distillation_runs',
            'mem_adapter_evals',
            'mem_policy_routes'
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

COMMIT;
