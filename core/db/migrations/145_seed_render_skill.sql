-- 145_seed_render_skill.sql - seed the render-html-to-prores skill.
--
-- The design engine's finishing half: turn an authored HTML graphic into a
-- ProRes/mp4 by running it through the baked headless-Chrome renderer
-- (/opt/render/render.js in the workspace image) and handing the produced file
-- to media_job so it lands in the Media tab + Library. The renderer is a real
-- baked mechanic (not prose); this skill is judgment about HOW to render
-- (duration, fps, resolution, transparent overlay vs full-frame, virtual-time
-- vs realtime capture). Pairs with design-html-graphic. Idempotent.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'render-html-to-prores',
  'Render an authored HTML graphic to a ProRes/mp4 video via the baked headless-Chrome renderer, surfaced through media_job into the Media tab + Library. The finishing half of the design engine; pair with design-html-graphic.',
  'low',
  '[]'::jsonb,
  '["render this graphic","render the graphic to video","export the graphic","turn this into a video","render to prores","render to mp4","make a video from this html","export the overlay"]'::jsonb,
  '[{"name":"html_path","type":"string","required":false,"doc":"the authored graphic''s HTML file in the workspace; usually from design-html-graphic"}]'::jsonb,
  '[{"name":"video","type":"object","doc":"the rendered ProRes/mp4 in the Media tab + Library"}]'::jsonb,
  0.78,
  'active',
  'manual',
  78,
  'Closes the design loop (author -> preview -> video) the boss asked for; the renderer is baked, this is the judgment to drive it.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'render-html-to-prores',
  '1.0.0',
  $skill$---
name: render-html-to-prores
version: "1.0.0"
description: Render an authored HTML graphic to ProRes/mp4 via the baked headless-Chrome renderer, surfaced through media_job into the Media tab.
trigger_phrases:
  - render this graphic
  - render the graphic to video
  - export the graphic
  - turn this into a video
  - render to prores
  - render to mp4
  - make a video from this html
inputs:
  - name: html_path
    type: string
    required: false
    doc: the authored graphic's HTML file in the workspace
outputs:
  - name: video
    type: object
    doc: the rendered ProRes/mp4 in the Media tab + Library
risk_level: low
network_egress: none
confidence: 0.78
---

# Render an HTML graphic to video

A graphic authored by `design-html-graphic` (or any HTML you point at) becomes a
ProRes/mp4 here. The renderer is baked into the cloud workspace at
`/opt/render/render.js` — you do NOT write a renderer; you drive it with the
right settings and hand the result to `media_job`. Cloud-side; needs the cloud
workspace (the renderer + chromium + ffmpeg live there).

## 1. Settle the render settings

From the graphic and the boss's intent:
- **duration** (seconds) and **fps** (30 default; 60 for ultra-smooth) — match
  the graphic's timeline `DURATION`.
- **resolution** — `--width`/`--height` (1920x1080 default; 4096x2160 for DCI 4K).
- **overlay vs full-frame** — a lower-third / bug / any graphic meant to sit over
  footage is an **overlay**: pass `--transparent` (alpha ProRes 4444, `.mov`).
  A full-frame scene is opaque (`.mp4`).
- **capture mode** — default virtual-time (deterministic, correct for CSS
  `@keyframes`). If the graphic leans on CSS *transitions* (transform/opacity
  transitions) or a JS-state timeline, those snap under virtual time — add
  `--realtime` for natural motion.
- **card** — if the graphic is a multi-card system, pass `--card <data-card id>`
  to render one card.

## 2. Render through media_job

Pick an output path in the workspace, then call `media_job` (never run the
renderer directly — media_job tracks it and surfaces the file):

    media_job(
      label: "Rendering <graphic name>",
      command: "source /workspace/.jarvis/env.sh && node /opt/render/render.js --html <abs html path> --out <abs out path> --duration <s> --fps 30 --width 1920 --height 1080 [--card <id>] [--transparent] [--realtime]",
      result: "output_files",
      output_glob: "<the same out path>"
    )

`media_job` runs it on the cloud bridge, picks up the produced file, saves it to
the Library, and shows it in the Media tab.

## 3. Report

Say what you rendered and that it's in the Media tab. If it's an alpha overlay,
say so (it composites over footage). If the motion looked snappy/jittery, re-run
with `--realtime`.

## Hard rules

- Always go through `media_job` (result=output_files) — never `node render.js`
  raw, or the asset won't be tracked or surfaced.
- Cloud workspace only. If it's unreachable, say so and stop.
- Match `--duration` to the graphic's real timeline; a too-short render clips the
  exit, too-long freezes on the last frame.
$skill$,
  '',
  0.78,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('render-html-to-prores', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
