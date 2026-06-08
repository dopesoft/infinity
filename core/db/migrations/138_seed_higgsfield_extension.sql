-- 138_seed_higgsfield_extension.sql - seed the higgsfield CLI as a known,
-- cloud-resident extension so media generation works without the boss's Mac.
--
-- higgsfield is a Go binary (homebrew tap on Mac; a curl install on Linux).
-- There is NO API-key env path - auth is device-OAuth that persists to
-- ~/.config/higgsfield/credentials.json. On the cloud workspace, env.sh pins
-- HOME to the Railway volume (/workspace/.jarvis), so both the install and the
-- credentials survive redeploys. That maps exactly onto the existing kind=cli
-- self-provision + human-in-the-loop flow (extensions/cli.go):
--
--   boot/first-use → install (curl) → check_cmd fails (not authed)
--     → auth_cmd prints a device URL → row parks status='pending_auth'
--     → ExtensionAuthChecklist re-probes each heartbeat tick
--     → boss signs in once in a browser → flips to 'active', forever.
--
-- The install URL is the vendor's own (INSTALL_FOR_AGENTS.md, verified). No
-- secret lands here - auth_envs is empty because higgsfield uses device-OAuth,
-- not an API key. Seeding enabled=true means LoadAll activates it on the next
-- boot (installs + parks pending_auth) when the cloud workspace is up; if the
-- workspace is asleep, LoadAll skips it cleanly and the next use re-probes.
--
-- Idempotent: ON CONFLICT (name) DO NOTHING never clobbers a row the agent has
-- since re-registered or authed.

BEGIN;

INSERT INTO mem_extensions
  (name, kind, description, config, enabled, source, status, auth_instructions)
VALUES (
  'higgsfield',
  'cli',
  'Higgsfield AI image + video generation CLI (cloud-resident). Text-to-image, image-to-video, and Soul-ID identity. Hand the crafted generate command to the media_job tool so the asset is tracked, downloaded, and shown in the Media tab.',
  $cfg${
    "install": "curl -fsSL https://raw.githubusercontent.com/higgsfield-ai/cli/main/install.sh | sh -s -- --prefix=$HOME/.local",
    "binary": "higgsfield",
    "check_cmd": "higgsfield account status",
    "auth_cmd": "higgsfield auth login",
    "auth_envs": [],
    "usage": "Generate media non-interactively: higgsfield generate create <model> --prompt \"...\" [--aspect_ratio 16:9] [--image <ref>] [--start-image <ref>] [--duration 5] [--soul-id <id>] --wait --json . Models -- image: gpt_image_2 (default), nano_banana_2, text2image_soul_v2; video: kling3_0 (cheap), seedance_2_0 (SOTA). --wait blocks and the --json stdout carries result_urls. Do NOT run this directly: hand the full command to media_job (result=stdout_urls) so the run is tracked and the asset lands in the Media tab + Library."
  }$cfg$::jsonb,
  TRUE,
  'manual',
  'pending_auth',
  'First use installs the higgsfield CLI on the cloud workspace and asks you to sign in once via a device URL. After that it stays authenticated.'
)
ON CONFLICT (name) DO NOTHING;

COMMIT;
