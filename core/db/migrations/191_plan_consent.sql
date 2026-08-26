-- 191_plan_consent.sql - plans start as proposals; consent is a fact.
--
-- 2026-08-26: the boss attached a book and said "don't build, let's discuss".
-- Jarvis made a plan on the first attempt (13:54) without him ever seeing it,
-- and on the second attempt resumed that same plan from memory. Nothing in the
-- substrate recorded whether a plan had ever been approved, so "resume where
-- you left off" (a virtue for crons) became "hammer a nail he never agreed to".
--
-- approved_at is the fact. A plan created in an interactive turn that is not
-- a clear work order is status='proposed' (approved_at NULL): not executable,
-- not in the chat dock, never adopted by a later session. plan_approve (the
-- boss says go) or the Studio "Go ahead" button flips it to active and stamps
-- approved_at. Legacy plans (crons, pre-migration chats) are backfilled as
-- approved so autonomous work keeps its continuity.
--
-- mem_intent_decisions.stance records what the classifier read each message
-- as (discuss | work | unclear), so a misread is auditable in the IntentStream.

ALTER TABLE mem_plans ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;

UPDATE mem_plans
   SET approved_at = created_at
 WHERE approved_at IS NULL
   AND status <> 'proposed';

CREATE INDEX IF NOT EXISTS idx_mem_plans_proposed
    ON mem_plans (session_id, updated_at DESC) WHERE status = 'proposed';

ALTER TABLE mem_intent_decisions ADD COLUMN IF NOT EXISTS stance TEXT;
