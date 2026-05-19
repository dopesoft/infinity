-- 059_unify_skill_versions.sql - normalize the skill version format.
--
-- Background: two code paths produced two different version shapes:
--   Loader fallback + extractor templates → semver ('0.0.1', '0.1.0')
--   Voyager auto-evolution (nextVersionStamp) → timestamp ('20260519-031200')
-- Both wrote into mem_skill_versions.version side by side, so the Studio
-- detail surface showed garbled mixed shapes ('v0.1.0', 'v20260519-…').
--
-- New canonical shape: v<MAJOR>.<MINOR>-<M-D-YYYY>  (e.g. v1.0-5-19-2026)
--
-- This migration resets every skill's history to a single row at
-- v1.0-5-19-2026 and points the active pointer at it. Old version rows
-- are dropped because they carry incompatible labels and the boss
-- explicitly asked for a fresh start ("lets just start all of them as
-- v1.0"). mem_skill_runs gets its version column rewritten to the same
-- value so the existing run history still resolves to a real version.
--
-- Going forward both the loader fallback and Voyager's bumper produce
-- versions in the new shape; see core/internal/skills/version.go.

BEGIN;

-- ── mem_skill_versions: keep one row per skill, label it v1.0-5-19-2026 ──
-- Pick the most-recent version per skill (by created_at DESC) and
-- rewrite that row's label. Delete the rest so the UNIQUE
-- (skill_name, version) constraint isn't violated by duplicates.
WITH ranked AS (
    SELECT id,
           skill_name,
           ROW_NUMBER() OVER (PARTITION BY skill_name ORDER BY created_at DESC) AS rn
      FROM mem_skill_versions
),
keepers AS (
    SELECT id, skill_name FROM ranked WHERE rn = 1
),
deletions AS (
    DELETE FROM mem_skill_versions v
     USING ranked r
     WHERE v.id = r.id
       AND r.rn > 1
    RETURNING v.skill_name
)
UPDATE mem_skill_versions v
   SET version = 'v1.0-5-19-2026'
  FROM keepers k
 WHERE v.id = k.id;

-- ── mem_skill_active: point every active pointer at the new label ────────
UPDATE mem_skill_active
   SET active_version = 'v1.0-5-19-2026',
       updated_at     = NOW()
 WHERE active_version IS DISTINCT FROM 'v1.0-5-19-2026';

-- ── mem_skill_runs: rewrite version on every historical run ──────────────
-- Runs persist (we don't want to lose execution history) but their
-- `version` foreign-key-ish reference must align with the new label
-- so the Studio "Runs" tab can group/filter by version meaningfully.
UPDATE mem_skill_runs
   SET version = 'v1.0-5-19-2026'
 WHERE version IS NOT NULL
   AND version <> 'v1.0-5-19-2026';

COMMIT;
