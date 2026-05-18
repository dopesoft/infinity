-- 051_suppress_low_value_skill_questions.sql
--
-- Defense-in-depth while older Core binaries may still be running:
-- suppress low-value "make this a skill?" questions generated from repeated
-- single-tool/substrate-only signatures. The Go detector now skips these, but
-- this trigger keeps the live dashboard clean before/through deploy.

BEGIN;

CREATE OR REPLACE FUNCTION suppress_low_value_skill_question()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    q text := lower(COALESCE(NEW.question, ''));
BEGIN
    IF NEW.source_kind = 'skill_opportunity'
       AND (
           q LIKE '%composio__gmail_fetch_emails%'
           OR q LIKE '%surface_item -> surface_item%'
           OR q LIKE '%system_map -> load_tools%'
           OR q LIKE '%surface_item -> notify%'
       )
    THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_suppress_low_value_skill_question ON mem_curiosity_questions;
CREATE TRIGGER trg_suppress_low_value_skill_question
BEFORE INSERT ON mem_curiosity_questions
FOR EACH ROW
EXECUTE FUNCTION suppress_low_value_skill_question();

UPDATE mem_curiosity_questions
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       resolved_reason = 'auto_quiet_051',
       auto_dismissed_at = NOW()
 WHERE status = 'open'
   AND source_kind = 'skill_opportunity'
   AND (
       question ILIKE '%composio__GMAIL_FETCH_EMAILS%'
       OR question ILIKE '%surface_item -> surface_item%'
       OR question ILIKE '%system_map -> load_tools%'
       OR question ILIKE '%surface_item -> notify%'
   );

COMMIT;
