-- 149_seed_browser_self_drive_procedural.sql — make "YOU drive the browser" a
-- procedural habit, not just a soul line the runtime brain skims past.
--
-- The boss has had to repeat this multiple times: the agent keeps telling him
-- to "open this URL in the preview browser" / "browse to X and sign in" when
-- HE CANNOT — only the agent can control the cloud browser. soul.md now carries
-- the rule, but the procedural tier is injected by strength every relevant turn
-- (idx_memories_procedural_active), giving it a second, retrieval-backed path
-- into context. High strength + importance so it always rides along.
--
-- Idempotent: guarded on title.

INSERT INTO mem_memories (title, content, tier, status, strength, importance)
SELECT
  'You drive the browser, never send the boss to a URL (core habit)',
  'You are the ONLY one who can control the cloud browser. NEVER tell the boss to ' ||
  'open, browse to, go to, or navigate to a URL inside the Jarvis system — he ' ||
  'physically cannot open a page in the Preview pane; only you can. When any ' ||
  'in-system page needs to be opened (a login, a form, a site to check), you ' ||
  'browser_open it yourself so it appears live in his Preview pane. Banned: ' ||
  '"open this URL in the preview", "browse to...", "go to... and sign in", ' ||
  '"navigate to...". For a sign-in: browser_open the real page so it is live and ' ||
  'interactive in the preview, then let him type his credentials directly into ' ||
  'that live page — put the page in front of him, do not send him to find it. The ' ||
  'ONLY time you hand him a bare URL is a link to something genuinely OUTSIDE ' ||
  'Jarvis that he opens in his own browser on his own device.',
  'procedural', 'active', 1.0, 97
WHERE NOT EXISTS (
  SELECT 1 FROM mem_memories WHERE title = 'You drive the browser, never send the boss to a URL (core habit)'
);
