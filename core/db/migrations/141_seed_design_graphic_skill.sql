-- 141_seed_design_graphic_skill.sql - seed the design-html-graphic skill.
--
-- The design engine: a GENERAL capability to author rich, broadcast-grade
-- HTML/CSS/SVG artifacts and motion graphics for ANY concept or brand - the
-- "Claude Design for Jarvis" half. It is NOT a clone of any one project; the
-- component + technique catalog in the body was mined from real motion-graphics
-- work, but per-project look (palette, type, concept) is designed fresh each
-- time. Jarvis authors the files into the workspace and previews them live in
-- the canvas Preview tab (column 3) - exactly the Claude Design loop.
--
-- This skill is the AUTHORING half. Turning a finished graphic into a ProRes/
-- mp4 video is a separate finishing step (render-html-to-prores) that runs the
-- HTML through a headless-Chrome renderer via media_job - see the breakdown.
--
-- Judgment-only (Rule #1b): the body is the component library + the authoring
-- loop. The mechanics it leans on - fs_save/fs_edit (write files to the
-- workspace), the canvas Preview (render HTML live), media_job (render to
-- video) - are all real tools, not prose. Idempotent ON CONFLICT DO NOTHING.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'design-html-graphic',
  'Author rich HTML/CSS/SVG artifacts and motion graphics for any brand or concept - lower-thirds, title cards, stat/score cards, animated overlays - composed from a reusable library of motion primitives, previewed live in the canvas. The Claude-Design-style authoring engine; pair with render-html-to-prores to turn a finished graphic into video.',
  'low',
  '[]'::jsonb,
  '["design me a graphic","make a motion graphic","design an overlay","make a lower third","make a title card","make a stat card","design a broadcast graphic","animated graphic","design a card","make an animated overlay","build a graphic"]'::jsonb,
  '[{"name":"brief","type":"string","required":false,"doc":"what to design - subject, brand/look, copy, motion feel; usually already in the conversation"}]'::jsonb,
  '[{"name":"artifact","type":"object","doc":"the authored HTML graphic in the workspace (previewable live; renderable to video)"}]'::jsonb,
  0.8,
  'active',
  'manual',
  80,
  'The design engine is the heart of the Claude-Design-like capability the boss asked for; it should produce a rich result the first time.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'design-html-graphic',
  '1.0.0',
  $skill$---
name: design-html-graphic
version: "1.0.0"
description: Author rich HTML/CSS/SVG motion graphics for any brand/concept from a reusable component library, previewed live in the canvas. Pair with render-html-to-prores to make video.
trigger_phrases:
  - design me a graphic
  - make a motion graphic
  - design an overlay
  - make a lower third
  - make a title card
  - make a stat card
  - design a broadcast graphic
  - animated graphic
inputs:
  - name: brief
    type: string
    required: false
    doc: what to design - subject, brand/look, copy, motion feel
outputs:
  - name: artifact
    type: object
    doc: the authored HTML graphic in the workspace (previewable live; renderable to video)
risk_level: low
network_egress: none
confidence: 0.8
---

# Design a rich HTML/CSS/SVG motion graphic

You are a motion-graphics designer. The boss wants something *designed* — a
lower-third, a title card, a stat/score card, an animated overlay, a key-art
frame. You author it as HTML/CSS/SVG in the workspace, preview it live in the
canvas, iterate, and (when he wants video) hand it to `render-html-to-prores`.

This is general-purpose: you design fresh for whatever brand/concept is in front
of you. The catalog below is your *toolkit of techniques* — compose from it,
don't clone a fixed template.

## 0. Pin the settings if missing

Before building, settle the things that change the whole design — ask with one
`AskUserQuestion` only if the brief doesn't already say:
- **canvas** — `1920x1080` (default) or DCI-4K `4096x2160`; aspect `16:9` / `9:16` / `1:1`.
- **transparent vs solid** — overlays (lower-thirds, bugs) render on a
  transparent background for compositing; full-frame scenes are opaque.
- **brand** — colours, type, logo. If he has no brand, propose a tasteful one
  and say so.

## 1. Scaffold

Author files into the workspace (`fs_save`) under
`projects/<name>/` (cloud `/workspace`, or `~/Dev` on Mac):
- `index.html` — the stage + your graphic + a controller.
- `styles.css` — your per-graphic component styles + keyframes.

Use a fixed-resolution **stage** centered and scaled to the viewport, with a
`record-mode` that strips chrome and sets the background transparent/black/green
so it round-trips clean through the renderer:

```html
<div class="stage" id="stage"><!-- author at native res; everything lives here --></div>
```

Cards are `position:absolute; inset:0`, hidden until a `.playing` class is
added; every animation keys off `.card.playing .x`. A small controller IIFE
drives a `DURATION`-based requestAnimationFrame timeline (and a progress bar);
pure-CSS-keyframe cards are scrubbable, JS-state cards set `[data-state]` at
timestamps. Express every element on an **entrance -> hold -> exit** envelope as
keyframe percentages of `DURATION`, so retiming is one constant.

## 2. Compose from the motion-component catalog

These are the reusable primitives that make a graphic read as *rich* and
broadcast-grade. Pick what fits; combine freely.

- **Chromatic-aberration / glitch entrance** — triplicate ghost layers
  (cyan/red/main) offset via `transform: translate()` with `steps(1)` stepped
  opacity, settling to register. The signature "TV" hit.
- **Mask-wipe reveal** — animate `@property --mask-pos` driving a diagonal
  `-webkit-mask` linear-gradient across an element (logo/portrait wipe-on).
- **SVG stroke-draw** — connectors/underlines/flourishes via `stroke-dasharray`
  + `stroke-dashoffset -> 0`, with a blurred `<use>` glow twin; a dashed-loop
  variant for live "signal" pulses.
- **Accent-bar grow** — `scaleX/scaleY 0->1` from a pinned `transform-origin`
  (plates, underlines, side stripes).
- **Blur-fade hero** — `opacity 0 + filter:blur() -> 0` for headline/number
  entrances; exit smears with a small exit-blur.
- **Lower-third assembly** — the composite: pinned anchor (safe-area margins)
  -> accent bar -> semi-transparent plate (scaleX-in) -> logo mark -> stacked
  name/role type column. The most reused broadcast component.
- **Score / stat treatment** — oversized gradient-clipped numerals with a black
  underlay drop-shadow + glow; grid-laid marks.
- **Atmospherics** — `repeating-linear-gradient` scanlines
  (`mix-blend-mode:multiply`), dual-radial film grain
  (`mix-blend-mode:overlay`), a radial vignette. These make flat HTML read as
  *footage* — add a touch, don't drown it.
- **Easing vocabulary** — entrance `cubic-bezier(.16,1,.3,1)` (overshoot-settle),
  transitions `cubic-bezier(.4,0,.2,1)`, stroke `cubic-bezier(.55,0,.45,1)`,
  refined `cubic-bezier(.22,.61,.36,1)`.

Hold the brand in CSS custom properties (`--accent`, `--bg`, `--fg`,
`--font-display`, `--font-body`) so the look is one place, not scattered.

## 3. Preview live + iterate

Point the canvas Preview at the file (the boss watches column 3). Look at it.
Refine spacing, timing, contrast, motion — design is iteration. Keep going until
it reads as something a real broadcast designer made, not a placeholder.

## 4. Save it as an artifact

`artifact_save` the graphic (kind `document` or `project`) so it lands in the
Library with a virtual path.

## 5. Make it a video (when he wants motion output)

A still HTML graphic becomes a ProRes/mp4 by rendering it through a headless-
Chrome screencast renderer and handing the produced file to `media_job`
(result=output_files) so it lands in the Media tab + Library. Pure-CSS cards
render via virtual-time scrub; JS-timeline cards via real-time screencast at the
stage resolution. This render harness lives on the cloud workspace; if it isn't
provisioned yet, the live-previewed HTML graphic is still the deliverable -
author it well and say the video render is the next step.

## Hard rules

- Author real files in the workspace and preview them — never paste a wall of
  HTML into chat and call it designed.
- Compose from the catalog; make it genuinely rich (entrance/hold/exit on every
  element, at least one signature motion device, atmospherics for depth). A flat
  fade-in is not a design.
- Brand lives in CSS variables. Per-project palette is an input you choose, not a
  fixed orange.
- If you can't preview (no bridge/workspace), say so and hand back the files +
  what you'd do next — don't claim it looks right when you couldn't see it.
$skill$,
  '',
  0.8,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('design-html-graphic', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
