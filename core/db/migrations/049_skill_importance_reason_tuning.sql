-- 049_skill_importance_reason_tuning.sql - tighten skill value labels after 048.

BEGIN;

UPDATE mem_skills
   SET importance_reason = CASE name
        WHEN 'load-and-invoke-skill' THEN 'Core tool-use loop: loads dormant capabilities and uses the matching skill without wasting a turn.'
        WHEN 'batch-surface-items' THEN 'Executive-assistant automation: turns multiple findings into ranked dashboard surface items consistently.'
        WHEN 'false-positive-curiosity-triage' THEN 'Core quality loop: prevents benign empty results from polluting proactive escalation.'
        WHEN 'surprise-output-triage' THEN 'Core learning loop: compares surprising outputs to expectations and updates behavior.'
        WHEN 'targeted-file-read-loop' THEN 'Useful coding discipline: narrows context before reading files to reduce noise and cost.'
        WHEN 'ship-code-to-main' THEN 'Shipping workflow with side effects; useful when explicitly requested, not autonomous cognition.'
        WHEN 'create_habit_pursuit' THEN 'Dashboard action recipe for habit tracking; useful but narrow.'
        WHEN 'gmail-triage-update' THEN 'Pending/merged update body for Gmail triage; useful only as a refinement trail.'
        ELSE importance_reason
       END,
       importance = CASE name
        WHEN 'targeted-file-read-loop' THEN 70
        WHEN 'ship-code-to-main' THEN 75
        ELSE importance
       END,
       risk_level = CASE
        WHEN name IN ('batch-surface-items','create_habit_pursuit','ship-code-to-main') THEN 'medium'
        ELSE risk_level
       END
 WHERE name IN (
    'load-and-invoke-skill',
    'batch-surface-items',
    'false-positive-curiosity-triage',
    'surprise-output-triage',
    'targeted-file-read-loop',
    'ship-code-to-main',
    'create_habit_pursuit',
    'gmail-triage-update'
 );

UPDATE mem_skill_proposals p
   SET importance = s.importance,
       importance_reason = s.importance_reason,
       risk_level = s.risk_level
  FROM mem_skills s
 WHERE p.name = s.name
   AND p.name IN (
    'load-and-invoke-skill',
    'batch-surface-items',
    'false-positive-curiosity-triage',
    'surprise-output-triage',
    'targeted-file-read-loop',
    'ship-code-to-main',
    'create_habit_pursuit',
    'gmail-triage-update'
   );

COMMIT;
