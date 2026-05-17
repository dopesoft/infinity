-- 048_skill_importance.sql - separate execution risk from strategic importance.
--
-- risk_level answers "how dangerous is execution / which sandbox?"
-- importance answers "how fundamental is this skill to AGI-like behavior?"

BEGIN;

ALTER TABLE mem_skills
    ADD COLUMN IF NOT EXISTS importance INT NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS importance_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE mem_skill_proposals
    ADD COLUMN IF NOT EXISTS importance INT NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS importance_reason TEXT NOT NULL DEFAULT '';

UPDATE mem_skills
   SET importance = CASE
        WHEN name IN (
            'autoskill-repair',
            'self-improve-from-finding',
            'propose-skill-from-pattern',
            'evolve-skill-from-deviation',
            'memory-ground-then-act',
            'remember-load-recall-loop',
            'load-and-invoke-skill',
            'resolve-connector-identities',
            'resolve_connected_account_identities'
        ) THEN 95
        WHEN name IN (
            'gmail-triage',
            'triage_emails_to_dashboard',
            'batch-surface-items',
            'surprise-output-triage',
            'false-positive-curiosity-triage',
            'load-then-sweep-gmail',
            'repeat-gmail-fetch-sweep'
        ) THEN 85
        WHEN name LIKE 'scaffold-%' THEN 65
        ELSE GREATEST(importance, 60)
       END,
       importance_reason = CASE
        WHEN name IN ('autoskill-repair','self-improve-from-finding','propose-skill-from-pattern','evolve-skill-from-deviation')
          THEN 'Core self-improvement loop: notice failures or patterns and turn them into better skills.'
        WHEN name IN ('memory-ground-then-act','remember-load-recall-loop')
          THEN 'Core memory-grounding loop: recall context and persist durable lessons before acting.'
        WHEN name IN ('resolve-connector-identities','resolve_connected_account_identities')
          THEN 'Core connector identity loop: makes tool/account routing reliable across future turns.'
        WHEN name IN ('gmail-triage','triage_emails_to_dashboard','load-then-sweep-gmail','repeat-gmail-fetch-sweep')
          THEN 'Executive-assistant automation: reads inbox state and surfaces important follow-up work.'
        WHEN name LIKE 'scaffold-%'
          THEN 'Useful coding/build capability, but not part of the agent cognition core.'
        ELSE COALESCE(NULLIF(importance_reason, ''), 'General reusable skill.')
       END
 WHERE importance_reason = '' OR importance = 50;

UPDATE mem_skill_proposals p
   SET importance = s.importance,
       importance_reason = s.importance_reason
  FROM mem_skills s
 WHERE p.name = s.name
   AND (p.importance = 50 OR p.importance_reason = '');

UPDATE mem_skill_proposals
   SET importance = 90,
       importance_reason = 'Skill update for an existing active capability; review for behavior improvement.'
 WHERE parent_skill <> ''
   AND (importance = 50 OR importance_reason = '');

CREATE INDEX IF NOT EXISTS idx_mem_skills_importance
    ON mem_skills (importance DESC, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_mem_skill_proposals_importance
    ON mem_skill_proposals (status, importance DESC, created_at DESC);

COMMIT;
