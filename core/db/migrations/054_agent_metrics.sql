-- 054_agent_metrics.sql - the agent's self-model rolled up daily.
--
-- mem_agent_metrics is what closes the metacognition loop: the critic
-- already scores sessions (mem_reflections.quality_score), the predict-then-
-- act loop already records surprise (mem_predictions.surprise_score), the
-- cost ledger already tracks spend (mem_cost_events). What was missing was
-- a daily rollup the agent can read to notice itself drifting - "my cost
-- per turn is 3x normal today", "this skill's success rate has been
-- declining for a week", "surprise is up across the board".
--
-- The SelfModelProvider reads this table once per turn and emits a
-- <self_model_alert> block ONLY when any metric is >2σ off its 14-day
-- trailing baseline. Silent on normal days; loud when something has
-- actually changed.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_agent_metrics (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    day          DATE NOT NULL,
    -- Free-form so new metrics can be added without a migration. Today:
    --   avg_quality_score | cost_usd_total | avg_surprise
    --   skill_success_rate | tool_error_rate
    metric_name  TEXT NOT NULL,
    -- Empty for whole-agent metrics; skill name for skill_success_rate;
    -- tool name for tool_error_rate. Lets one metric_name carry many
    -- per-subject rows without a separate table per dimension.
    dimension    TEXT NOT NULL DEFAULT '',
    value        DOUBLE PRECISION NOT NULL,
    sample_size  INT NOT NULL DEFAULT 0,
    -- 14-day trailing baseline. NULL until 7+ samples exist.
    baseline_14d DOUBLE PRECISION,
    sigma_14d    DOUBLE PRECISION,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (day, metric_name, dimension)
);

CREATE INDEX IF NOT EXISTS idx_mem_agent_metrics_recent
    ON mem_agent_metrics (metric_name, dimension, day DESC);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_agent_metrics'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_agent_metrics;
        END IF;
    END IF;
END $$;

COMMIT;
