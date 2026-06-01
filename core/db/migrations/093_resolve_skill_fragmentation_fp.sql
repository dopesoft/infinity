-- 093_resolve_skill_fragmentation_fp.sql — clear the false-positive
-- skill-fragmentation curiosity card.
--
-- Why: the skill_fragmentation detector (shipped last session) used a fuzzy
-- keyword matcher that mis-clustered the broad "google-workspace" skill with
-- "inbox-triage" and raised a "duplicate skills" card — a non-duplicate. It
-- also raw-INSERTed, bypassing the dismissal cooldown, so it reappeared after
-- the boss dismissed it. The Go fix switches detection to deterministic
-- name-based clustering and routes raising through the cooldown-aware
-- UpsertQuestion path. This migration clears the live open card now so it
-- doesn't linger until the next consolidate run.
--
-- Idempotent: re-running resolves nothing once already resolved.

BEGIN;

UPDATE mem_curiosity_questions
SET status = 'resolved', resolved_at = NOW()
WHERE source_tag = 'skill_fragmentation' AND status = 'open';

COMMIT;
