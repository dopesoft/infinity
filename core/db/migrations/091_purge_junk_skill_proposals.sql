-- 091_purge_junk_skill_proposals.sql — clear the auto-generated noise out of
-- the skill proposal queue so accepting a proposal can't recreate junk skills.
--
-- Why: the boss had 48 open candidates; 47 were machine noise — 46 "triplet_*"
-- rows (the discovery hook minting a proposal for any repeated tool sequence,
-- including degenerate ones like claude_code__bash×3 / fs_read×3 that are just
-- ordinary agent mechanics) plus 1 "session_pattern_*" stub (the LLM-unavailable
-- placeholder). Promoting any of them would add a meaningless skill to the
-- catalog. The discovery.go guard added alongside this migration stops new ones
-- from being minted; this clears the backlog. The one legitimate candidate
-- (cloud-workspace-update → active parent cloud-workspace) is left for the boss.
--
-- Also archives a "session_pattern_*" stub that had been promoted to an ACTIVE
-- skill — same placeholder junk, just already live.
--
-- Reversible: proposals are set to 'rejected' (not deleted); the skill is
-- soft-retired (status='archived').

BEGIN;

-- Reject auto-generated triplet + session-pattern candidates.
UPDATE mem_skill_proposals
SET status = 'rejected', decided_at = NOW()
WHERE status IN ('candidate', 'pending', 'draft')
  AND (name LIKE 'triplet_%' OR name LIKE 'session_pattern_%');

-- Archive any session_pattern_* stub that slipped through to an active skill.
UPDATE mem_skills
SET status = 'archived'
WHERE status = 'active' AND name LIKE 'session_pattern_%';

COMMIT;
