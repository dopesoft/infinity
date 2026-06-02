-- 114_dismiss_self_internal_curiosity.sql — clear the noise curiosity questions
-- the graph cleanup kicked up.
--
-- The boss: "there are 8 questions jarvis is asking... youve mentioned boss
-- before, whats so important about that... youve mentioned inbox triage... get
-- rid of those or resolve them."
--
-- These are all source_kind='uncovered_mention': the detector flags any graph
-- node with many mentions and no backing memory. Migrations 108/110 pruned the
-- derived memories but left the entity nodes, so the boss himself ("boss",
-- "user", kai@dopesoft.io), his own connected accounts ("malabie industries
-- account", "mr khaya account"), Infinity table names ("mem_curiosity_questions")
-- and skill names ("inbox-triage", "self-improve-from-finding") all showed up as
-- "unfilled knowledge gaps." None is a real gap — the agent already knows what a
-- skill or the boss is.
--
-- This dismisses every OPEN uncovered_mention question and parks a long cooldown
-- so it can't re-surface before the detector-side fix (shouldSuppressUncovered-
-- Mention in core/internal/proactive/curiosity.go) deploys. After deploy the
-- detector never generates these again. Idempotent.

BEGIN;

UPDATE mem_curiosity_questions
   SET status            = 'dismissed',
       auto_dismissed_at = NOW(),
       resolved_at       = NOW(),
       resolved_reason   = 'noise: self/internal entity (boss, his own accounts, table/skill names) surfaced by uncovered_mention after the graph cleanup — not a real knowledge gap',
       cooldown_until    = NOW() + INTERVAL '100 years'
 WHERE status = 'open'
   AND source_kind = 'uncovered_mention';

COMMIT;
