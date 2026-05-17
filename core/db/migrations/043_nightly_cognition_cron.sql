-- 043_nightly_cognition_cron.sql
--
-- Add the deterministic `system_task` cron kind and seed the default
-- nightly cognition job. The job is substrate: Core dispatches it to
-- maintenance.RunNightlyCognition, which reuses typed reflection,
-- consolidation, compression, Gym extraction, and generic surface report
-- writers. No shell-out, no prompt-vertical.

BEGIN;

ALTER TABLE mem_crons DROP CONSTRAINT IF EXISTS mem_crons_job_kind_check;
ALTER TABLE mem_crons
    ADD CONSTRAINT mem_crons_job_kind_check
    CHECK (job_kind IN ('system_event', 'isolated_agent_turn', 'connector_poll', 'system_task'));

INSERT INTO mem_crons
    (name, schedule, schedule_natural, job_kind, target, target_config,
     enabled, max_retries, backoff_seconds)
VALUES
    (
      'nightly-cognition',
      '0 4 * * *',
      'nightly at 4am UTC',
      'system_task',
      'nightly_cognition',
      '{
        "task": "nightly_cognition",
        "options": {
          "reflect_window": "24h",
          "reflect_limit": 20,
          "compress": true,
          "compress_batch": 50,
          "gym_limit": 100
        }
      }'::jsonb,
      TRUE,
      1,
      300
    )
ON CONFLICT (name) DO UPDATE SET
    schedule = EXCLUDED.schedule,
    schedule_natural = EXCLUDED.schedule_natural,
    job_kind = EXCLUDED.job_kind,
    target = EXCLUDED.target,
    target_config = EXCLUDED.target_config,
    enabled = EXCLUDED.enabled,
    max_retries = EXCLUDED.max_retries,
    backoff_seconds = EXCLUDED.backoff_seconds;

COMMIT;
