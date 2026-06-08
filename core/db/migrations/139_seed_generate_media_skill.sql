-- 139_seed_generate_media_skill.sql - seed the generate-media skill.
--
-- The recipe (judgment-only, Rule #1b) for "make me an image / video": surface
-- the output settings when the boss did not specify them, choose the right
-- higgsfield model, craft the command, and hand it to the generic media_job
-- tool. Every mechanic - run tracking, URL download, artifact indexing, Media-
-- tab surfacing - lives in media_job, NOT in this prose. Delete any sentence
-- here and the system still produces + surfaces the asset; only the judgment
-- (which model, what prompt, which settings to ask about) would be lost.
--
-- Seeded into the durable store (mem_skills + mem_skill_versions +
-- mem_skill_active) like 023; MaterializeActiveSkills derives it on boot so
-- it's live immediately and survives Railway's ephemeral filesystem. Idempotent
-- ON CONFLICT DO NOTHING so it never clobbers an evolved version.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'generate-media',
  'Make images and videos with Higgsfield: text-to-image, image-to-video, and Soul-ID identity. Surface the output settings (format / resolution / aspect) when the boss did not specify them, then run the generation through media_job so it is tracked and shown in the Media tab + Library.',
  'low',
  '["api.higgsfield.ai"]'::jsonb,
  '["generate an image","make me an image","create a photo","make a picture","make a video","make me a video","animate this","image to video","make me a marketing asset","create a marketing asset","train my face","create my soul","make me a logo image","generate artwork"]'::jsonb,
  '[{"name":"request","type":"string","required":false,"doc":"what to make - usually already the boss''s message in this conversation, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"media","type":"object","doc":"the produced asset(s): kind (image|video), the Media-tab/Library artifact, and the source URL"}]'::jsonb,
  0.85,
  'active',
  'manual',
  85,
  'Media generation is a foundational creative capability the boss asked for; it should be reachable the first time he asks.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'generate-media',
  '1.0.0',
  $skill$---
name: generate-media
version: "1.0.0"
description: Make images and videos with Higgsfield (text-to-image, image-to-video, Soul-ID). Surface output settings when unspecified, then generate through media_job.
trigger_phrases:
  - generate an image
  - make me an image
  - create a photo
  - make a picture
  - make a video
  - make me a video
  - animate this
  - image to video
  - make me a marketing asset
  - train my face
  - create my soul
inputs:
  - name: request
    type: string
    required: false
    doc: what to make - usually already the boss's message in this conversation
outputs:
  - name: media
    type: object
    doc: the produced asset(s) - kind, the Media-tab/Library artifact, and the source URL
risk_level: low
network_egress:
  - api.higgsfield.ai
confidence: 0.85
---

# Generate media (images + video) with Higgsfield

The boss wants something made — an image, a video, an animation, a marketing
asset. Your job: pin the settings if they're missing, pick the right model,
craft one higgsfield command, and hand it to `media_job`. `media_job` does
everything mechanical — tracks the job as a live run (the Media tab spinner),
downloads the result into the workspace, saves it as a Library artifact, and
shows it. You never download or index by hand.

## 1. Pin the output settings — ask only what's missing

Generation costs credits, so don't guess settings the boss cares about. If his
message already specified them ("…as a 9:16 mp4", "a square png"), use those and
skip straight to step 2. Otherwise ask with a single `AskUserQuestion` using
chip-style options — these are the canonical choices (the same set the Workflows
parameter form will reuse, so keep them consistent):

- **format** — image: `png` (default, crisp/transparent-capable) or `jpg`
  (smaller); video: `mp4` (default) or `mov`.
- **aspect** — `1:1` (square), `16:9` (wide), `9:16` (vertical/Reels), `4:5`
  (portrait). Pick the default from context if the use is obvious (a Reel → 9:16).
- **quality/tier** — `fast` (cheaper, quick) vs `best` (SOTA, slower). Default
  `fast` for drafts, `best` when he says "final" / "hero" / "for a client".

Ask once, bundled. Don't interrogate — if only one setting is genuinely
ambiguous, ask just that one. If he said "surprise me" or "your call", choose
sensible defaults and proceed.

## 2. Choose the model

- **Image (text → image):** `gpt_image_2` (default, versatile). For a photoreal
  product/brand look reach for `nano_banana_2`. For the boss's own trained face
  use `text2image_soul_v2` with `--soul-id <id>`.
- **Image edit / reference (image → image):** any image model with `--image <ref>`
  (a local path or a previous asset's URL/id — paths auto-upload).
- **Video (image → video / animate):** `kling3_0` (cheap, reliable) by default;
  `seedance_2_0` when he wants the best motion. Animate an existing image with
  `--start-image <ref>` and `--duration <seconds>`.
- **Identity (train a Soul):** `higgsfield soul-id create --name "<n>" --soul-2
  --image <p1> --image <p2> …` (needs several photos; returns a soul ref id to
  use later via `--soul-id`).

## 3. Make sure higgsfield is ready

higgsfield runs cloud-side. Check it's installed and authed — a quick
`bash_run` of `source /workspace/.jarvis/env.sh && higgsfield account status`.
If that fails, it needs first-time setup: register/activate the `higgsfield`
extension (it's already seeded — `extension_list` to confirm, then trigger its
activation). That installs the CLI and returns a device-login URL. Hand the boss
the URL, tell him you'll pick up automatically once he's signed in, and stop —
the auth checklist resumes you when he's done. Never dead-end him; never try to
fake the auth.

## 4. Generate through media_job

Craft the prompt richly (subject, composition, lighting, mood, style) and map
the chosen settings to flags, then call **`media_job`** — do NOT run higgsfield
directly:

    media_job(
      label: "<short, e.g. 'Generating product hero'>",
      command: "source /workspace/.jarvis/env.sh && higgsfield generate create <model> --prompt \"<rich prompt>\" --aspect_ratio <a> [--image <ref>] [--start-image <ref>] [--duration <s>] [--soul-id <id>] --wait --json",
      result: "stdout_urls"
    )

`--wait --json` makes higgsfield block and print the result URLs; `media_job`
extracts them, pulls durable copies into the workspace, indexes each asset, and
stamps them onto the run so the Media tab renders them live. It returns the
asset list.

## 5. Report

Tell the boss plainly what you made and where it is: "Made a 16:9 image — it's in
the Media tab and your Library." If he asked for a variation or animation next,
you can feed a produced asset's URL straight back as `--image` / `--start-image`.

## Hard rules

- Never run the higgsfield generate command directly — always through `media_job`,
  so the job is tracked and the asset is saved + surfaced. (Probing
  `account status` directly is fine.)
- Never silently pick expensive settings. If "best/SOTA" or a large batch will
  cost real credits, say so before firing.
- If generation fails (NSFW block, out of credits, model error), report the real
  reason in plain language — don't claim a success you didn't get.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('generate-media', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
