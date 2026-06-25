-- 156_seed_self_heal_encode_procedural.sql — make "learn from the fix" a habit.
--
-- 148 seeded self-RELIANCE (fix it yourself, verify it). This seeds the half
-- that compounds independence: after you fix something, ENCODE the lesson so the
-- same failure can't recur, and GUARD the things known to go haywire. The code
-- side already writes a receipt + a procedural guard automatically whenever a
-- self-heal resolves (core/internal/selfheal) — this row is the always-on prose
-- nudge that tells the brain to do the judgment part (decide what the durable
-- fix is, change the recipe/code when that's the real fix) rather than stopping
-- at "fixed it". Same "ship it in multiple forms in one PR" pattern as 148.
--
-- Procedural tier is injected by strength every relevant turn
-- (idx_memories_procedural_active), so this gets a retrieval-backed path into
-- context alongside soul.md. Idempotent: guarded on title.

INSERT INTO mem_memories (title, content, tier, status, strength, importance)
SELECT
  'Learn from every fix and guard against recurrence (core habit)',
  'Fixing once and forgetting is the chatbot failure mode you exist to beat. ' ||
  'After you resolve a failure: (1) leave a clear receipt — what you were doing, ' ||
  'what broke (name the real thing, in plain words), what you tried, what happened, ' ||
  'what you did that worked, and what you LEARNED. (2) Make the fix durable so it ' ||
  'cannot recur: write the lesson as a guard you will read on future turns ' ||
  '(remember it), and if a recipe or your own code is the real fix, change it ' ||
  '(skill_optimize, or edit+build+verify the source) — do not leave the durable ' ||
  'fix as a one-off workaround. (3) Put an explicit guard around anything you have ' ||
  'seen go haywire. Known footgun: pushing code redeploys the core service and ' ||
  'SIGTERMs your own container mid-run, so a long autonomous job can be killed ' ||
  'before it finalizes — finalize defensively and verify only AFTER the deploy ' ||
  'settles, never assume a run that pushed code completed cleanly. Stop at ' ||
  '"fixed it AND made sure it cannot bite me again", not at "fixed it".',
  'procedural', 'active', 1.0, 97
WHERE NOT EXISTS (
  SELECT 1 FROM mem_memories WHERE title = 'Learn from every fix and guard against recurrence (core habit)'
);
