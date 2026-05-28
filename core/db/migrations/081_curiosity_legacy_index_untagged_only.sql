-- 081_curiosity_legacy_index_untagged_only.sql
--
-- Stops the recurring `UpsertQuestion insert ... duplicate key value violates
-- unique constraint "uq_curiosity_open_question" (SQLSTATE 23505)` warning that
-- fires every heartbeat in prod.
--
-- Root cause: the high-surprise curiosity detector builds the question TEXT from
-- the tool name ("Tool composio__GOOGLECALENDAR_EVENTS_LIST returned something
-- unexpected…") but builds the dedup source_tag from the prediction UUID. A
-- second surprising prediction for an already-seen tool therefore produces
-- identical question text under a NEW source_tag: the tag-keyed UPDATE/upsert
-- (uq_curiosity_open_by_tag) misses, the INSERT proceeds, and it collides on the
-- legacy question-text index uq_curiosity_open_question — forever.
--
-- The legacy (question)-text unique index was only ever intended as a fallback
-- for UNTAGGED detectors (see migration 040's comment). For tagged rows, dedup
-- is owned by uq_curiosity_open_by_tag(source_tag). Narrowing the legacy index
-- to untagged rows only removes the false collision while preserving question-
-- text dedup for the no-tag legacy path (insertQuestionLegacy writes source_tag=''
-- and relies on ON CONFLICT DO NOTHING, which still matches this predicate).

DROP INDEX IF EXISTS uq_curiosity_open_question;

CREATE UNIQUE INDEX IF NOT EXISTS uq_curiosity_open_question
    ON mem_curiosity_questions (question)
    WHERE status = 'open' AND (source_tag IS NULL OR source_tag = '');
