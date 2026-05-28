-- 080_archive_legacy_skill_versions.sql
--
-- System-wide version-history declutter. 35 skills carry 2-9 visible versions
-- each (78 rows total) — almost all legacy residue from before the canonical
-- vX.Y-M-D-YYYY naming existed (semver "1.0.0", timestamp "20260519-191840",
-- and superseded date stamps). Only the ACTIVE version per skill is ever loaded
-- into the registry (store.go joins mem_skill_active), so these extra rows never
-- reach the agent — they only clutter Studio's per-skill version history, which
-- the boss is strict about.
--
-- This soft-retires every NON-active version (archived_at = NOW()), leaving
-- exactly one visible version per skill: the active one. Fully reversible
-- (UPDATE ... SET archived_at = NULL) and non-destructive — rollback still works
-- because PromoteVersion finds archived rows by (skill_name, version) and now
-- clears archived_at when it activates one (store.go change in this PR).
--
-- Runtime-harmful duplicates were already handled by 075/076: every legacy
-- triage / gmail / *-update SKILL is status='archived'; the only active triage
-- skill is inbox-triage. This migration is purely history hygiene, no skill or
-- active-version changes.

BEGIN;

UPDATE mem_skill_versions v
SET archived_at = NOW()
FROM mem_skill_active a
WHERE a.skill_name = v.skill_name
  AND v.version <> a.active_version
  AND v.archived_at IS NULL;

COMMIT;
