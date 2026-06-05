-- 134_frame_mandate_active_version_wording.sql — fix the verification wording on
-- EVERY version of frame-the-mandate, including the Voyager-evolved variant that
-- became active the same day (v1.0-6-5-2026) and paraphrased the seed's claim to
-- "a second model to verify". Verification runs on the boss's OWN selected model
-- (his ChatGPT subscription) — there is no second/different model. Idempotent
-- substring replaces; safe to re-run.

BEGIN;

UPDATE mem_skill_versions
   SET skill_md = replace(
                    replace(skill_md,
                      'a high-stakes mandate needs a second model to verify',
                      'a high-stakes mandate needs an independent verification pass'),
                      'a second model to verify',
                      'an independent verification pass')
 WHERE skill_name = 'frame-the-mandate'
   AND skill_md LIKE '%second model%';

COMMIT;
