-- 039_skill_proposal_drafts.sql - one active draft per parent_skill with
-- compound merge + conflict detection.
--
-- The bug this closes: today the voyager extractor / optimizer / agent
-- skill_propose tool all INSERT plain rows into mem_skill_proposals
-- without any "is there already a candidate for this skill?" check.
-- Result: 7-8 open candidates pile up for the same parent_skill, each
-- proposing a different slice of changes. Boss approves them one by one
-- and the body interleaves in nonsense ways because each Decide() merge
-- ignores the prior pending edits.
--
-- The fix is structural: enforce ONE open candidate per parent_skill at
-- the DB layer (partial unique index), and on every new proposal the
-- application code MERGES into the existing draft rather than inserting
-- a new row. The draft accumulates: changes_log JSONB tracks every
-- reasoning entry, revision INT counts how many merges produced the
-- current body, conflicts JSONB lists any contradictions the merge LLM
-- flagged. Approving once applies the bundle.
--
-- Brand-new skill proposals (parent_skill IS NULL) keep the old INSERT
-- path - those genuinely SHOULD be separate. The constraint only kicks
-- in when there's a parent to merge against.
--
-- Idempotent.

BEGIN;

ALTER TABLE mem_skill_proposals
    ADD COLUMN IF NOT EXISTS changes_log        JSONB        NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS revision           INT          NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS conflicts          JSONB        NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS parent_proposal_id UUID,
    ADD COLUMN IF NOT EXISTS last_merged_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS proposal_kind      TEXT         NOT NULL DEFAULT 'standalone';

-- Adopt at most one existing non-frontier candidate per parent_skill as
-- the active draft. Older duplicates stay reviewable as standalone rows
-- instead of blocking the unique index.
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

-- One open draft per parent_skill. Partial index so NULL parent
-- (brand-new skills), standalone legacy candidates, and GEPA frontier
-- candidates are unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS uq_skill_proposals_open_draft
    ON mem_skill_proposals (parent_skill)
    WHERE status = 'candidate'
      AND parent_skill IS NOT NULL
      AND proposal_kind = 'draft';

COMMIT;
