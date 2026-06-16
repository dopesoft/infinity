-- 150_fix_self_provision_browser_drive.sql
--
-- The cloud-workspace skill (patched by migration 120) still tells Jarvis to
-- "hand the boss the auth URL it returns" when an extension needs sign-in. That
-- is the exact behavior the boss keeps catching and hates: he CANNOT open a URL
-- inside the Jarvis system -- only Jarvis can, via the Preview-pane browser
-- (browser_open). This is live skill prose injected into context, so it
-- overrides the soul rule + procedural memory 149 unless we fix it at the source.
--
-- Fix (Rule #1b): the mechanic is "open the sign-in page in the boss's Preview
-- pane yourself"; the seeded skill must say so, not the opposite. The matching
-- tool-result messages in core/internal/extensions/tools.go were corrected in
-- the same change so every path tells Jarvis to drive the browser.
--
-- Matches only the dash-free tail sentence so it does not depend on the em-dash
-- byte upstream. Idempotent: the NOT LIKE guard makes a re-run a no-op.

BEGIN;

UPDATE mem_skill_versions
SET skill_md = replace(
  skill_md,
  $old$hand the boss the auth URL it returns and it picks up automatically once he's in.$old$,
  $new$do NOT hand the boss the auth URL it returns -- he cannot open it himself. Open it in his Preview pane with browser_open so the live sign-in page appears in front of him and he signs in right there; it picks up automatically once he's in.$new$
)
WHERE skill_name = 'cloud-workspace'
  AND skill_md LIKE '%hand the boss the auth URL it returns%'
  AND skill_md NOT LIKE '%Open it in his Preview pane with browser_open so the live sign-in page%';

COMMIT;
