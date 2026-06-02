-- 095_mem_runs_human_error.sql — boss-facing failure explanations.
--
-- Why: mem_runs.error holds the RAW provider/tool failure string
-- ("openai_oauth: status=401 body={...token_revoked...}") and Studio renders it
-- verbatim. The boss reads runs, not logs, and that JSON tells him nothing about
-- what broke or what to do. runs.Finish now also writes a deterministic
-- translation (errs.Humanize) into this column: {category,title,summary,impact,
-- action,raw}. The raw `error` column is left untouched so nothing downstream
-- breaks; the UI prefers human_error.title and keeps raw on an expander.
--
-- mem_runs is already whole-row realtime-published, so this column replicates
-- to useRuns()/RunIndicator with no publication change.
--
-- Idempotent.

BEGIN;

ALTER TABLE mem_runs
  ADD COLUMN IF NOT EXISTS human_error JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMIT;
