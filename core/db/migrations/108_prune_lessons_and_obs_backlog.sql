-- 108_prune_lessons_and_obs_backlog.sql — prune the dead lesson bloat + the
-- observation trace backlog.
--
-- Code-rooted findings (this session):
--  - mem_lessons has ZERO read consumers (no provider injects it; the agent is
--    shaped by mem_memories / mem_training_examples / mem_reflection_chains). It
--    is a write-only archive, and forget.go never prunes it, so it grows forever.
--    Of 294, ~90 are redundant inbox-triage mechanics ("mint the batch_id" said 6
--    ways) now encoded in the inbox-triage v1.1 skill. Pure bloat.
--  - mem_observations trace rows (PreToolUse/PostToolUse/etc.) are meant to prune
--    at 24h, but the backlog is still here. Now that their memory-provenance was
--    deleted in the reset, prune the old ones immediately instead of waiting.
--
-- Keeps the genuinely useful GENERAL behavioral lessons (forbid-tool = hard
-- constraint, stop+escalate on 401, verify end-to-end before "done"). Idempotent.

BEGIN;

-- 1. Over-fitted inbox-triage mechanics: redundant, skill-encoded, behaviorally
--    dead. Gone.
DELETE FROM mem_lessons
 WHERE lesson_text ILIKE '%triage%'
    OR lesson_text ILIKE '%gmail%'
    OR lesson_text ILIKE '%inbox%'
    OR lesson_text ILIKE '%draft%'
    OR lesson_text ILIKE '%batch_id%'
    OR lesson_text ILIKE '%followup%'
    OR lesson_text ILIKE '%follow up%'
    OR lesson_text ILIKE '%surface_item%'
    OR lesson_text ILIKE '%dedup%'
    OR lesson_text ILIKE '%composio%'
    OR lesson_text ILIKE '%promotional%'
    OR lesson_text ILIKE '%newsletter%';

-- 2. Observation trace backlog: PreToolUse/PostToolUse/etc. older than 24h that
--    no surviving memory cites. (The forget loop does this nightly; we do it now
--    so the reset is immediately reflected, not 24h late.)
DELETE FROM mem_observations o
 WHERE o.hook_name IN ('PreToolUse','PostToolUse','ToolGated','SessionStart',
                       'SessionEnd','Stop','Notification','PreCompact',
                       'SubagentStart','SubagentStop','conversation_compaction')
   AND o.created_at < NOW() - INTERVAL '24 hours'
   AND NOT EXISTS (SELECT 1 FROM mem_memory_sources s WHERE s.observation_id = o.id);

COMMIT;
