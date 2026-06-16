-- 147_fix_higgsfield_extension.sql - repair the stranded higgsfield extension.
--
-- Migration 138 seeded higgsfield as a kind=cli device-OAuth extension, but it
-- used `ON CONFLICT (name) DO NOTHING`. A pre-existing row (created 2026-06-03
-- as kind=mcp, auth=bearer, $HIGGSFIELD_API_KEY empty, status=disabled) made
-- that a silent no-op, so the correct CLI config never landed. Every auth
-- attempt since then either tried the dead bearer path ("auth=bearer but
-- $HIGGSFIELD_API_KEY is empty") or had the agent hand-roll a brittle plan
-- step that failed over to the wrong bridge - and the CanvasAuthCard (which
-- only fires on status='pending_auth') never appeared.
--
-- This migration force-corrects the row to the device-OAuth CLI config from
-- 138. It is a deliberate UPDATE (not an idempotent DO NOTHING) precisely
-- because the whole bug was a DO NOTHING that refused to overwrite. It only
-- touches the higgsfield row and only when it's still mis-seeded as mcp /
-- bearer - if the agent has since re-registered a working row, the WHERE guard
-- leaves it alone.
--
-- Lesson recorded so we don't repeat it: a seed migration that ends in
-- `ON CONFLICT DO NOTHING` permanently strands every later correction.

BEGIN;

UPDATE mem_extensions
SET
  kind = 'cli',
  description = 'Higgsfield AI image + video generation CLI (cloud-resident). Text-to-image, image-to-video, and Soul-ID identity. Hand the crafted generate command to the media_job tool so the asset is tracked, downloaded, and shown in the Media tab.',
  config = $cfg${
    "install": "curl -fsSL https://raw.githubusercontent.com/higgsfield-ai/cli/main/install.sh | sh -s -- --prefix=$HOME/.local",
    "binary": "higgsfield",
    "check_cmd": "higgsfield account status",
    "auth_cmd": "higgsfield auth login",
    "auth_envs": [],
    "usage": "Generate media non-interactively: higgsfield generate create <model> --prompt \"...\" [--aspect_ratio 16:9] [--image <ref>] [--start-image <ref>] [--duration 5] [--soul-id <id>] --wait --json . Models -- image: gpt_image_2 (default), nano_banana_2, text2image_soul_v2; video: kling3_0 (cheap), seedance_2_0 (SOTA). --wait blocks and the --json stdout carries result_urls. Do NOT run this directly: hand the full command to media_job (result=stdout_urls) so the run is tracked and the asset lands in the Media tab + Library."
  }$cfg$::jsonb,
  enabled = TRUE,
  source = 'manual',
  status = 'pending_auth',
  last_error = '',
  auth_url = '',
  auth_instructions = 'First use installs the higgsfield CLI on the cloud workspace and asks you to sign in once via a device URL. After that it stays authenticated.',
  updated_at = NOW()
WHERE name = 'higgsfield'
  AND (kind <> 'cli' OR enabled = FALSE OR status = 'disabled' OR status = 'error');

COMMIT;
