-- 144_seed_finish_mac_skill.sql - seed the finish-on-mac skill (color grade +
-- DaVinci Resolve), the deliberately Mac-only finishing step.
--
-- Color grading (LUTs) and Resolve timeline/render work live on the boss's Mac:
-- the color-grade Claude Code skill (ruby LUT generators + ffmpeg apply) and the
-- davinci-resolve MCP both need the Mac (Resolve's GUI/node graph, the local
-- scripting bridge). Jarvis reaches them over the Mac bridge via claude_code -
-- they are NOT cloud-portable, which is why they're a separate finishing skill
-- rather than part of the cloud media pipeline.
--
-- Judgment-only body; the mechanics are the existing Mac tools. Idempotent.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'finish-on-mac',
  'Color-grade footage (generate + apply a .cube LUT) or do a DaVinci Resolve edit/render. Mac-only finishing: needs the Mac bridge + claude_code (the color-grade skill and davinci-resolve MCP live on the Mac).',
  'low',
  '[]'::jsonb,
  '["color grade this","grade the footage","make a LUT","apply a lut","cinematic color","fix the color","davinci resolve","edit in resolve","render in resolve","build a resolve timeline","finish the video"]'::jsonb,
  '[{"name":"target","type":"string","required":false,"doc":"the clip/footage or project to finish; usually already in the conversation"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"the graded clip or rendered timeline, with where it landed"}]'::jsonb,
  0.75,
  'active',
  'manual',
  70,
  'Finishing (grade + Resolve) is the Mac-side capstone of the multimedia stack; the agent should know it is reachable on the Mac bridge.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'finish-on-mac',
  '1.0.0',
  $skill$---
name: finish-on-mac
version: "1.0.0"
description: Color-grade footage (generate + apply a .cube LUT) or do a DaVinci Resolve edit/render. Mac-only - needs the Mac bridge + claude_code.
trigger_phrases:
  - color grade this
  - grade the footage
  - make a LUT
  - apply a lut
  - cinematic color
  - davinci resolve
  - edit in resolve
  - render in resolve
  - finish the video
inputs:
  - name: target
    type: string
    required: false
    doc: the clip/footage or project to finish
outputs:
  - name: result
    type: string
    doc: the graded clip or rendered timeline, with where it landed
risk_level: low
network_egress: none
confidence: 0.75
---

# Finish on the Mac - color grade + DaVinci Resolve

Color grading and Resolve editing are **Mac-only**. They need the boss's Mac:
the color-grade tooling and the DaVinci Resolve scripting bridge both live there
and have no cloud equivalent. So the first thing to check is the bridge.

## 0. Require the Mac bridge

This work only runs on the **Mac** bridge (via `claude_code`). If the session is
on the cloud bridge (or the Mac is offline), say so plainly and ask the boss to
bring the Mac online / pin the session to Mac - don't try to fake a grade or a
Resolve render in the cloud. Cloud-side media (higgsfield gen, html->video,
ffmpeg cuts) is a different skill; this is the Mac capstone.

## 1. Color grade (generate + apply a LUT)

The `color-grade` Claude Code skill lives on the Mac. Over `claude_code__Bash`:
- **Generate a LUT:** `ruby generate_lut.rb <type> <out.cube> [--strength=0.0-1.0]`
  (correction presets like `night_warm_fix`, creative chains like `studio_film`),
  or chain presets: `ruby generate_chain_lut.rb <out.cube> <preset@strength> ...`.
- **Auto-recommend from a frame:** `python3 auto_grade.py <frame.png>`.
- **Apply to video** (works cloud too, but the LUTs are authored here):
  `ffmpeg -i in.mp4 -vf "lut3d='look.cube':interp=tetrahedral" out.mp4`.
- Preview interactively via the skill's local http server when iterating.

Pick the grade from intent (warm/cinematic/clean/punchy); show the boss the
before/after frame, iterate on strength.

## 2. DaVinci Resolve (edit / timeline / render)

Resolve must be running on the Mac (Studio, with external scripting = Local).
Drive it through the `davinci-resolve` MCP tools (reachable on the Mac):
`media_pool` (import media, create timeline, append clips), `timeline` /
`timeline_item` (trim, move, grade), `graph` (color node chain, set LUTs),
`render` (add job, set preset, start). Typical flow: load/confirm project ->
import media -> create timeline -> append clips -> set render preset -> start
render -> report the output file.

## 3. Report

Say what you did and where the result is (the graded clip path, or the Resolve
render output). If you applied a LUT, name it. Plain language.

## Hard rules

- Mac bridge only. On cloud, surface that and stop - never claim a grade/render
  you couldn't run.
- Don't invent Resolve state - confirm the project/timeline via the MCP before
  acting.
- LUTs are reusable artifacts: `artifact_save` a good .cube so it's in the
  Library for next time.
$skill$,
  '',
  0.75,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('finish-on-mac', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
