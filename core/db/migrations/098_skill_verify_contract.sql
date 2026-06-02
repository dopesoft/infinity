-- 098_skill_verify_contract.sql — substrate for the skill VERIFICATION GATE.
--
-- The boss: "HOW DO WE BUILD A SKILL AND THEN WE DONT ENSURE IT CAN BRING BACK
-- DATA. IF THE FUCKIN SKILL CANT BE VERIFIED THEN THE TEST CLEANS ITSELF UP."
--
-- Until now a skill was promoted to 'active' on a REGEX over its SKILL.md text
-- (voyager.isAutoPromotable) — it never ran. This adds:
--   - mem_skills.verify_contract: skill-AUTHORED data (Rule #1) describing how to
--     prove the skill returns real data. Shape:
--       { "scenario": "<read-only task to run the skill on>",
--         "assert":   "<substring that must appear in the result, optional>",
--         "min_artifacts": <int, default 0> }
--     The scenario MUST be read-only (fetch/compute/report) — verification must
--     never surface, draft, send, or persist to the boss's dashboard. When the
--     column is empty the harness uses a sensible default assertion: the run
--     succeeded AND produced ≥1 non-empty data artifact under the test session.
--   - an index on mem_observations(session_id) so the harness's post-run
--     artifact count + self-cleanup (DELETE WHERE session_id = <test session>)
--     are fast.
--
-- Idempotent.

BEGIN;

ALTER TABLE mem_skills
  ADD COLUMN IF NOT EXISTS verify_contract JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_mem_observations_session_id
  ON mem_observations (session_id);

COMMIT;
