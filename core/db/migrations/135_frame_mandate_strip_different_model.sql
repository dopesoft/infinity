-- 135_frame_mandate_strip_different_model.sql — final wording fix. The
-- Voyager-evolved active version (v1.0-6-5-2026) retained the seed's
-- "a *different* AI model audit" sentence. Verification runs on the boss's OWN
-- model (his ChatGPT subscription) — never a different one. Use regexp_replace
-- so it catches the phrase regardless of how the line wraps. Idempotent.

BEGIN;

UPDATE mem_skill_versions
   SET skill_md = regexp_replace(
                    skill_md,
                    'a \*different\* AI\s+model audit',
                    'an independent verification pass to audit',
                    'g')
 WHERE skill_name = 'frame-the-mandate'
   AND skill_md ~ '\*different\* AI';

COMMIT;
