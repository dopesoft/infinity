-- 163_update_document_skill_themes.sql
--
-- Teach the make-document skill that deck THEMES are switchable. The renderer
-- (render_pptx.js) ships a default "dopesoft" brand theme plus built-in
-- recolors (emerald, royal, crimson, amber, slate) and a dark theme
-- (midnight), selectable via the spec's top-level `theme` (a name or a partial
-- override object). v1.2's §3 told the agent it must NEVER style a deck, which
-- made it ignore a "make it dark / use green" ask. This bumps to v1.3 and
-- carves out the exception — default stays auto-themed (no per-element jank),
-- but the boss's explicit look/color/dark-mode request is honored via `theme`.
--
-- Per Rule #1b this is JUDGMENT only (when to switch theme); the theme tokens +
-- rendering are mechanics in render_pptx.js.
--
-- NOTE: like 161, the renderer is baked into the workspace image, so the new
-- themes only render after a workspace image rebuild + redeploy. Until then a
-- `theme` is ignored and the deck renders with the default — a graceful degrade.
--
-- Safe + verifiable: derives the new body from the v1.2 row via replace(), and
-- only repoints mem_skill_active when v1.3 actually exists — so if 161 hasn't
-- been applied yet this migration is a clean no-op.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
SELECT
  'make-document',
  'v1.3-6-26-2026',
  replace(
    replace(
      skill_md,
      'version: "v1.2-6-25-2026"',
      'version: "v1.3-6-26-2026"'
    ),
    $old$A deck is **themed automatically** (brand logo, palette, title chrome) — you
NEVER describe colors, fonts, or styling.$old$,
    $new$A deck is **themed automatically** (brand logo, palette, title chrome), so
you never hand-style individual slides. The default is the dopesoft brand
theme; ONLY if the boss asks for a specific look, color, or **dark mode**, pass
a top-level `theme` to `document_create` — a built-in name (`emerald`, `royal`,
`crimson`, `amber`, `slate`, or `midnight` for dark) or a partial override like
`{"primary":"FF5A1F"}`. Otherwise omit `theme` entirely.$new$
  ),
  implementation,
  confidence,
  'manual'
FROM mem_skill_versions
WHERE skill_name = 'make-document' AND version = 'v1.2-6-25-2026'
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint active only if the new version was actually created.
UPDATE mem_skill_active
   SET active_version = 'v1.3-6-26-2026'
 WHERE skill_name = 'make-document'
   AND EXISTS (
     SELECT 1 FROM mem_skill_versions
      WHERE skill_name = 'make-document' AND version = 'v1.3-6-26-2026'
   );

COMMIT;
