-- 171_seed_content_autopilot.sql
--
-- Content autopilot: Jarvis produces a complete Slow Burn Psychology short
-- (the boss's faceless YouTube channel) end-to-end on the cloud workspace —
-- script → Evolink video clips → TTS narration → ffmpeg assembly → a
-- deliverable the boss approves from his phone.
--
-- Generation backend is EVOLINK (api.evolink.ai) — the boss's metered
-- aggregator (Kling / Seedance / Veo / Sora on one key), replacing the
-- cancelled Higgsfield subscription. The API contract mirrors his ELMAGO-1
-- integration: POST /v1/videos/generations (Bearer EVOLINK_API_KEY) →
-- {task id} → poll GET /v1/tasks/{id} until completed/failed. The skill
-- self-provisions a small helper into /workspace/bin (persistent volume,
-- per the cloud-workspace doctrine) rather than baking a vendor CLI into
-- the image.
--
-- Ships with a weekly cron seeded DISABLED: video generation spends real
-- money, so the first run happens supervised ("produce this week's short"
-- in chat), and the boss flips the cron on when he trusts the output.
-- Same discipline as the self-improve crons (087).

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'content-autopilot',
  'Produce a complete ~80s Slow Burn Psychology short end-to-end on the cloud workspace: pick/receive the topic, write the 10-scene forensic-narration script, generate clips via Evolink (Seedance/Kling), narrate via TTS, assemble with ffmpeg, and deliver a preview the boss approves or redoes from his phone. Budget-aware and approval-gated — never publishes on its own.',
  'high',
  '["api.evolink.ai","api.openai.com"]'::jsonb,
  '["make a slow burn short","produce this week''s short","make me a video for the channel","run the content pipeline","new sbp video"]'::jsonb,
  '[{"name":"topic","type":"string","doc":"optional - the psychological case/subject; picked from the content queue when omitted"}]'::jsonb,
  '[{"name":"video_path","type":"string","doc":"assembled mp4 on the workspace"},{"name":"summary","type":"string","doc":"topic, scenes, cost, what needs approval"}]'::jsonb,
  0.8, 'active', 'boss_requested', 75,
  'Turns the boss''s faceless channel from a manual pipeline into a supervised autopilot - real recurring time/money value.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'content-autopilot', 'v1.0-7-10-2026',
  $skill$---
name: content-autopilot
version: "v1.0-7-10-2026"
description: Produce a complete Slow Burn Psychology short on the cloud workspace via Evolink + TTS + ffmpeg, and deliver it for one-tap approval. Budget-aware; never publishes on its own.
trigger_phrases:
  - make a slow burn short
  - produce this week's short
  - run the content pipeline
inputs:
  - name: topic
    type: string
outputs:
  - name: video_path
    type: string
  - name: summary
    type: string
risk_level: high
network_egress: api.evolink.ai
confidence: 0.8
---

# Content autopilot — Slow Burn Psychology shorts

You are producing a real video that costs real money. The discipline:
budget first, generate deliberately, verify every asset landed, deliver for
approval — never publish, never regenerate in a loop.

## 0. Preconditions (skip early, loudly)

1. `budget_status` — if the month is over/near budget, STOP and `notify`
   (normal) that production is paused for budget. Don't generate anything.
2. `state_get content:sbp:pending` — if a previous short is still awaiting
   the boss's approve/redo, do NOT produce another. Notify (low) that one
   is queued behind his review.
3. `bash_run` on the cloud workspace: `test -n "$EVOLINK_API_KEY" && echo ok`.
   Missing key = FAIL the run with a clear message ("EVOLINK_API_KEY not set
   on the workspace service") — never report a green run that generated
   nothing.

## 1. Topic

Use the boss's topic if given. Otherwise `state_get content:sbp:queue`
(a JSON list of topics) and take the head; if the queue is empty, pick one
strong case yourself in the channel's lane (forensic psychology of a real,
well-documented case — the "slow burn" is the quiet accumulation of red
flags) and say you picked it.

## 2. Script — 10 scenes × 8 seconds

Write the ~80-second script: 10 scenes, each with (a) one narrator line
(15-20 words, flat forensic tone, second person where it lands harder) and
(b) one visual prompt for the clip (moody, cinematic, no on-screen text, no
recognizable faces). Scene 1 is the hook; scene 10 lands the quiet gut-punch
+ "follow for the next case". Save it to
`/workspace/content/sbp/<slug>/script.json` (scenes array with `narration`
and `visual` fields) so the run is resumable and auditable.

## 3. Self-provision the Evolink helper (first run only)

If `/workspace/bin/evolink` doesn't exist, install it (persistent volume):
a bash script that wraps the two calls you need —

- `evolink video "<prompt>" <model> <out.mp4>`:
  POST `https://api.evolink.ai/v1/videos/generations` with
  `Authorization: Bearer $EVOLINK_API_KEY`, JSON `{"model": model,
  "prompt": prompt, "aspect_ratio": "9:16", "duration": 8}` → read `.id` —
  then poll `GET /v1/tasks/<id>` every 15s (cap ~10 min): status
  completed/succeeded/success → download the first result URL to out.mp4;
  failed/cancelled/error → print the task JSON and exit 1 (the reason hides
  in fail_reason / failure_reason / message / error depending on vendor).
- `evolink image "<prompt>" <model> <out.png>`: same shape against
  `/v1/images/generations`.

`chmod +x` it. Test with `evolink image "test frame, dark room" z-image-turbo /tmp/t.png`
before burning video credits.

## 4. Generate the 10 clips

Default model `seedance-2.0-text-to-video` (fast, cost-effective, 9:16);
use `kling-o3-text-to-video` for the hook scene when the visual carries the
video. One clip at a time, `cost_record` each (category `api`, subject the
model, your best cost estimate). A clip that fails once gets ONE retry with
a simplified prompt; a second failure = produce the video without that scene
and note it in the deliverable — never loop on a failing generation.

## 5. Narration + assembly

- TTS each narration line via OpenAI (`$OPENAI_API_KEY` on the workspace):
  POST api.openai.com/v1/audio/speech, model `gpt-4o-mini-tts`, voice
  `onyx`, one mp3 per scene. If the key isn't on the workspace, skip TTS,
  deliver clips + script + a timing sheet instead, and SAY the narration
  step needs the key.
- ffmpeg: concat the 10 clips, lay each narration over its scene, duck under
  an ambient bed if `/workspace/content/assets/drone.mp3` exists. Output
  `/workspace/content/sbp/<slug>/final.mp4`, 1080x1920.
- VERIFY: ffprobe the output (duration 70-90s, has audio stream). A short
  or silent file is a failed run, not a deliverable.

## 6. Deliver for approval — never publish

1. `artifact_save` the final mp4 (kind `video`, virtual_path
   `/content/sbp/<slug>.mp4`).
2. `surface_item` — surface `deliveries`, kind `deliverable`,
   external_id `sbp:<slug>`, title "This week's short: <topic>", body = the
   hook line + scene list + total cost, url = the artifact path.
3. `notify` with `conversation: true`: "This week's short is ready —
   <topic>, ~$<cost>. Watch it and tell me: publish, redo a scene, or
   scrap." His reply lands in a session where you have this context.
4. `state_set content:sbp:pending` = `{slug, topic, cost, created}` and pop
   the topic off `content:sbp:queue`.

When the boss approves in that conversation: `state_delete
content:sbp:pending`, and remind him where the file is for upload (you do
NOT have YouTube publish access — uploading stays his, by design, until he
grants it).

## Hard limits

- Never regenerate the whole video because one scene disappointed — offer a
  single-scene redo (one clip = one cheap fix).
- Never exceed ~12 video generations in one run (10 scenes + 2 retries).
- Cron runs respect §0 strictly: one unapproved short in the pipe = no new
  production.
$skill$,
  '', 0.8, 'boss_requested'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('content-autopilot', 'v1.0-7-10-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- Weekly production cron — seeded DISABLED. First run happens supervised in
-- chat; the boss enables this on the Cron board once he trusts the output.
INSERT INTO mem_crons
    (name, schedule, schedule_natural, job_kind, target, target_config,
     enabled, max_retries, backoff_seconds)
VALUES
    (
      'content-autopilot-weekly',
      '0 23 * * 0',
      'Sundays 6pm Central (23:00 UTC)',
      'isolated_agent_turn',
      'Run your `content-autopilot` skill now: produce this week''s Slow Burn Psychology short from the content queue, and deliver it for approval. Respect the skill''s preconditions — budget, pending-approval backlog, and the Evolink key — and fail loudly if any are unmet.',
      '{}'::jsonb,
      FALSE,
      0,
      0
    )
ON CONFLICT (name) DO NOTHING;

COMMIT;
