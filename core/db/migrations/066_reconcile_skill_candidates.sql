-- 066_reconcile_skill_candidates.sql
--
-- Self-cleaning Candidate panel. A "create this skill" proposal in
-- mem_skill_proposals (parent_skill IS NULL) is now retired automatically the
-- moment a skill of the same name becomes active — see
-- skills.Store.UpsertSkill (every activation: chat install via Registry.Put,
-- boot-time Reload) and voyager.Manager.Decide (the Promote button, including
-- duplicate siblings). Previously the ONLY thing that cleared a candidate was
-- the Promote/Reject button, so installing a skill any other way left the
-- candidate stranded in the panel forever.
--
-- This migration does the one-time backfill for candidates stranded BEFORE
-- that logic shipped: any pending create-new candidate whose name already
-- exists as an active skill is moot, so mark it 'superseded'. This is a soft
-- status transition, not a delete — the row and its full SKILL.md stay for
-- audit; it just leaves the Candidate filter. Patch/improvement candidates
-- (parent_skill set: GEPA frontier, merged drafts, optimize) are intentionally
-- left pending because they target the already-active skill, not a creation.

COMMENT ON COLUMN mem_skill_proposals.status IS
  'candidate | promoted | rejected | superseded (auto-retired when a skill of the same name became active outside the Promote button)';

UPDATE mem_skill_proposals p
   SET status = 'superseded', decided_at = NOW()
  FROM mem_skills s
 WHERE p.status = 'candidate'
   AND p.parent_skill IS NULL
   AND s.name = p.name
   AND s.status = 'active';
