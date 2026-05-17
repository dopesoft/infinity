-- 042_fix_agi_loop_schema_wiring.sql
--
-- Migrations 039 and 041 were already applied in prod before their local
-- review fixes landed. This forward migration applies those corrections to
-- live databases:
--
-- 1. Replace the broad "one candidate per parent_skill" index with a draft-only
--    index so GEPA frontier candidates can still keep multiple rows.
-- 2. Backfill proposal_kind so the newest existing non-frontier candidate per
--    parent_skill is the active draft and older duplicates stay reviewable.
-- 3. Add the mem_predictions columns required by healer-emission predictions.
--
-- Idempotent.

BEGIN;

-- 039 correction: the original index was too broad and blocked GEPA frontier
-- rows, which intentionally insert multiple candidate rows for one parent_skill.
DROP INDEX IF EXISTS uq_skill_proposals_open_draft;

ALTER TABLE mem_skill_proposals
    ADD COLUMN IF NOT EXISTS proposal_kind TEXT NOT NULL DEFAULT 'standalone';

WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY parent_skill
               ORDER BY created_at DESC, id DESC
           ) AS rn
      FROM mem_skill_proposals
     WHERE status = 'candidate'
       AND parent_skill IS NOT NULL
       AND frontier_run_id IS NULL
)
UPDATE mem_skill_proposals p
   SET proposal_kind = CASE WHEN r.rn = 1 THEN 'draft' ELSE 'standalone' END
  FROM ranked r
 WHERE p.id = r.id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_skill_proposals_open_draft
    ON mem_skill_proposals (parent_skill)
    WHERE status = 'candidate'
      AND parent_skill IS NOT NULL
      AND proposal_kind = 'draft';

-- 041 correction: healer-emission predictions share mem_predictions with
-- tool-call predictions but need a type discriminator and scored_at timestamp.
ALTER TABLE mem_predictions
    ADD COLUMN IF NOT EXISTS kind      TEXT        NOT NULL DEFAULT 'tool_prediction',
    ADD COLUMN IF NOT EXISTS scored_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_predictions_kind_created
    ON mem_predictions(kind, created_at DESC);

COMMIT;
