-- 046_prompt_skill_proposal_get_nudge.sql
--
-- Migration 042 fixed the schema/tooling for one active skill draft per
-- parent_skill. This migration updates the persistent default self-authoring
-- skill so the agent's recipe matches the new contract: read any pending draft
-- with skill_proposal_get before calling skill_optimize.
--
-- Idempotent.

BEGIN;

UPDATE mem_skills
   SET description = 'When a skill_drift finding fires, compare the installed skill body to the divergent (successful) sequence, read any pending draft with skill_proposal_get, draft an updated SKILL.md, and call skill_optimize so the boss can review the merged diff inline in chat.',
       updated_at = NOW()
 WHERE name = 'evolve-skill-from-deviation';

UPDATE mem_skill_versions
   SET skill_md = REPLACE(
       REPLACE(
         REPLACE(
           skill_md,
           'description: When a skill_drift finding fires, compare the installed skill body to the divergent (successful) sequence, draft an updated SKILL.md, and call skill_optimize so the boss can review the diff inline in chat.',
           'description: When a skill_drift finding fires, compare the installed skill body to the divergent (successful) sequence, read any pending draft with skill_proposal_get, draft an updated SKILL.md, and call skill_optimize so the boss can review the merged diff inline in chat.'
         ),
         '## 3. Propose the optimization

Call `skill_optimize` with the updated full SKILL.md AND the parent skill name.
The tool inserts a row into `mem_skill_proposals` with `parent_skill` set, and
the SkillProposalCard in /live renders it with a "vs current" diff so the boss
can see exactly what''s changing.',
         '## 3. Merge with any pending draft

Call `skill_proposal_get` with the parent skill name. If it returns an open
candidate, merge that pending `skill_md` with your new evidence-backed change
before proposing. Do not overwrite pending edits from an earlier run unless
they directly conflict; keep both improvements when they are compatible.

## 4. Propose the optimization

Call `skill_optimize` with the updated full SKILL.md AND the parent skill name.
The tool merges into the one active draft for that parent skill, and the
SkillProposalCard in /live renders it with a "vs current" diff so the boss can
see exactly what''s changing.'
       ),
       '## 4. Confirm',
       '## 5. Confirm'
     )
 WHERE skill_name = 'evolve-skill-from-deviation'
   AND version = '1.0.0'
   AND skill_md NOT LIKE '%skill_proposal_get%';

COMMIT;
