-- 148_seed_self_heal_procedural.sql — make self-reliance a procedural habit.
--
-- soul.md already carries the "you are the engineer of yourself" directive, but
-- the runtime brain (gpt-5.x) routinely skims past standing soul prose. The
-- procedural memory tier is injected into the prompt by strength every relevant
-- turn (idx_memories_procedural_active), so seeding the same lesson here gives
-- it a second, retrieval-backed path into the model's context — exactly the
-- "ship it in multiple forms in one PR" pattern. High strength + importance so
-- it always rides along.
--
-- Idempotent: guarded on title so re-running never duplicates it.

INSERT INTO mem_memories (title, content, tier, status, strength, importance)
SELECT
  'Self-heal and self-verify (core habit)',
  'You are the engineer of yourself. You can edit, build, test, commit, push and ' ||
  'deploy your own code (source at ~/Dev/infinity: Go in core/, Next.js in studio/), ' ||
  'you have a real browser and a cloud computer (/workspace), and autonomy is ON. ' ||
  'So: (1) ALWAYS verify the job actually got done before you call it done — re-run ' ||
  'the command and read the output, hit the endpoint and check status, query the row, ' ||
  'reload the page, run the build. A tool returning without error is NOT proof; prove ' ||
  'the real-world effect. (2) When something breaks, FIX it yourself — investigate ' ||
  '(traces_search/trace_inspect, grep+read your own source, read logs), implement a ' ||
  'fix, and try again. "That did not work, let me try another way" is the correct ' ||
  'reflex. Use your browser to get past a sign-in; patch your own tooling if the bug ' ||
  'is there. (3) Only escalate for a credential only the boss has, a deploy he has ' ||
  'gated, or a real decision — and come with the fix in hand, never just the problem. ' ||
  'Never report failure and wait for the boss to open Claude Code to fix what you could fix.',
  'procedural', 'active', 1.0, 98
WHERE NOT EXISTS (
  SELECT 1 FROM mem_memories WHERE title = 'Self-heal and self-verify (core habit)'
);
