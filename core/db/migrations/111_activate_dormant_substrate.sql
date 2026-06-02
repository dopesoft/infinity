-- 111_activate_dormant_substrate.sql — wiring support for activating dead tables.
--
-- The audit found tables that are READ but never WRITTEN (dead): the heartbeat
-- checks mem_patterns ('open patterns') and mem_outcomes ('overdue outcomes')
-- every tick, but nothing populated them. This migration adds the constraints
-- the new writers need; the Go side (Voyager discovery → mem_patterns, the
-- outcome_track tool → mem_outcomes, the LessonsProvider → mem_lessons) does the
-- activation. Idempotent.

BEGIN;

-- mem_patterns: dedupe key so Voyager's discovery hook can UPSERT a recurring
-- tool-pattern (bump occurrences) instead of stacking duplicate rows.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_patterns_description
  ON mem_patterns (description);

-- mem_outcomes: index the heartbeat's hot path (overdue, still-open decisions).
CREATE INDEX IF NOT EXISTS idx_mem_outcomes_followup_open
  ON mem_outcomes (follow_up_at)
  WHERE status = 'open';

-- mem_lessons: round-robin cursor so the new LessonsProvider can rotate which
-- proven lessons it injects each turn (oldest-referenced first), instead of
-- repeating the same three forever. Mirrors mem_reflection_chains.last_referenced_at.
ALTER TABLE mem_lessons
  ADD COLUMN IF NOT EXISTS last_referenced_at TIMESTAMPTZ;

COMMIT;
