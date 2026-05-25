-- 076_consolidate_duplicate_skills.sql
--
-- Full skill-catalog de-duplication. The boss's rule: ONE skill per capability,
-- so the agent is never confused choosing between near-identical recipes. The
-- promotion path (voyager.Decide) and the canonicalIntents dedup now PREVENT
-- new duplicates; this migration cleans the accumulated ones.
--
-- Three overlap clusters collapse to a single canonical each (soft archive -
-- status flip, fully reversible). Plus two one-off dups already handled live,
-- included here so a fresh database is born in the same clean state. Genuinely
-- distinct skills (per-stack scaffolds, per-service connectors, GitHub verbs)
-- are intentionally left alone.
--
-- Canonical kept (NOT touched):
--   • validate-surprising-tool-output-before-rework  (surprising/empty tool output)
--   • memory-ground-then-act                          (recall + load before acting)
--   • autoskill-repair                                (finding/drift/failure -> improve)
--   • resolve-connector-identities                    (connected-account identity)
--
-- Idempotent: WHERE status='active' so re-running skips already-archived rows.

BEGIN;

UPDATE mem_skills
   SET status = 'archived', updated_at = NOW()
 WHERE status = 'active'
   AND name IN (
     -- cluster 1: "a tool returned something unexpected/empty - it's actually a
     -- valid success shape, so don't rework / don't escalate a curiosity question"
     'classify-valid-empty-tool-results',
     'dismiss-valid-empty-tool-results',
     'surprise-output-triage',
     'false-positive-curiosity-triage',
     -- cluster 2: "ground on memory + load the needed tools, then act"
     'remember-load-recall-loop',
     -- cluster 3: "a finding / skill drift / failure -> make a durable improvement"
     -- (autoskill-repair's description is the explicit superset of all three)
     'evolve-skill-from-deviation',
     'propose-skill-from-pattern',
     'self-improve-from-finding',
     -- one-offs already archived live; listed for fresh-DB reproducibility
     'resolve_connected_account_identities',
     'session_pattern_1779323493'
   );

-- Retire any open candidate proposals targeting the archived skills so they
-- don't reappear in the Candidate panel asking for approval.
UPDATE mem_skill_proposals
   SET status = 'superseded', decided_at = NOW()
 WHERE status = 'candidate'
   AND COALESCE(NULLIF(parent_skill, ''), name) IN (
     'classify-valid-empty-tool-results',
     'dismiss-valid-empty-tool-results',
     'surprise-output-triage',
     'false-positive-curiosity-triage',
     'remember-load-recall-loop',
     'evolve-skill-from-deviation',
     'propose-skill-from-pattern',
     'self-improve-from-finding',
     'resolve_connected_account_identities',
     'session_pattern_1779323493'
   );

COMMIT;
