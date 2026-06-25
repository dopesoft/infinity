-- 157_retire_agent_reach.sql - retire agent-reach as redundant.
--
-- We added agent-reach (154/155) before fully accounting for capability we
-- already had: gpt-5.4's built-in web search (general search), http_fetch
-- (normal page reads), the already-seeded yt-dlp extension (142, YouTube
-- transcripts), and the anti-detect camofox browser (065, paywalls + Twitter/
-- Reddit/LinkedIn logged-in). agent-reach overlaps all of these and adds nothing
-- unique: its search = built-in, its read = http_fetch/browser, its yt-dlp =
-- already seeded, its scrapers = worse than the browser (cookie upkeep + ban
-- risk + a young dependency). So it earns no lane. Retire it; the routing now
-- lives in soul.md as a cheapest-tool-first ladder ending at the browser.
--
-- SOFT + REVERSIBLE - nothing is dropped:
--   * the agent-reach extension is disabled (enabled=false), so LoadAll skips it.
--     The installed binary on the cloud volume is left in place, harmless.
--   * the web-reach skill is archived and its active pointer removed, so
--     MaterializeActiveSkills stops deriving it. The skill rows + body remain in
--     mem_skills / mem_skill_versions and can be re-published by re-inserting the
--     mem_skill_active pointer if we ever want it back.
-- Idempotent.

BEGIN;

-- Disable the agent-reach cli extension (don't delete the row or the install).
UPDATE mem_extensions
   SET enabled = FALSE, status = 'disabled', updated_at = NOW()
 WHERE name = 'agent-reach';

-- Unpublish the web-reach skill: archive the catalog row and drop the active
-- pointer so it no longer materializes into the live registry. Versions/body
-- are retained for reversibility.
UPDATE mem_skills
   SET status = 'archived', updated_at = NOW()
 WHERE name = 'web-reach';

DELETE FROM mem_skill_active
 WHERE skill_name = 'web-reach';

COMMIT;
