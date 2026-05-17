-- 045_skill_proposal_api_indexes.sql
--
-- Indexes for the richer skill-proposal API filters added with the
-- AGI self-evolution Studio view. No schema shape change.

BEGIN;

CREATE INDEX IF NOT EXISTS idx_skill_proposals_frontier
    ON mem_skill_proposals(frontier_run_id, pareto_rank)
    WHERE frontier_run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_proposals_parent_status_kind
    ON mem_skill_proposals(parent_skill, status, proposal_kind)
    WHERE parent_skill IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_proposals_kind_created
    ON mem_skill_proposals(proposal_kind, created_at DESC);

COMMIT;
