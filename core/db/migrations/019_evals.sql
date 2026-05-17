-- 019_evals.sql - the verification substrate.
--
-- Phase 4 of the assembly substrate. As the agent assembles more - skills,
-- workflows, runtime tools - it must be able to prove its assemblies
-- actually work, and notice when one regresses. mem_evals is the generic
-- outcome ledger: every skill run, workflow run, or tool use can record an
-- outcome here, and a scorecard rolls them up into a success rate + a
-- recent-vs-historical trend so a degrading capability is visible.
--
-- The agent records via eval_record / reads via eval_scorecard - never raw
-- SQL. The workflow engine auto-records every run on completion.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_evals (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- WHAT was evaluated. subject_kind ∈ skill | workflow | tool |
    -- extension (free-form so a new assembled-thing kind doesn't need a
    -- migration); subject_name is its name.
    subject_kind  TEXT NOT NULL,
    subject_name  TEXT NOT NULL,

    -- Optional pointer to the concrete run this outcome came from
    -- (a mem_workflow_runs id, a mem_skill_runs id, …).
    run_id        TEXT NOT NULL DEFAULT '',

    -- success | failure | partial.
    outcome       TEXT NOT NULL,
    -- Optional 0-100 quality score - for outcomes that aren't binary.
    score         SMALLINT,
    -- What happened / why - the qualitative signal.
    notes         TEXT NOT NULL DEFAULT '',

    -- engine = auto-recorded by the workflow engine · agent = the agent's
    -- own judgment · boss = the boss graded it.
    source        TEXT NOT NULL DEFAULT 'agent',

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The scorecard's hot query: "every eval for subject X, newest first."
CREATE INDEX IF NOT EXISTS idx_mem_evals_subject
    ON mem_evals (subject_kind, subject_name, created_at DESC);

-- Realtime: Studio subscribes so scorecards live-update.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_evals'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_evals';
        END IF;
    END IF;
END $$;

COMMIT;
