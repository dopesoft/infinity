-- 143_seed_shorts_workflow.sql - seed the download-transcribe-shorts workflow.
--
-- The boss's concrete example: "download a YouTube video, then make transcribed
-- shorts of the best parts." Expressed entirely in the existing durable engine
-- as a saved mem_workflows definition with declared inputs. The engine runs the
-- four steps in FIXED ORDER (LLM out of the loop between steps); judgment is
-- quarantined to the one select step. Repeatable, resumable, parameterized.
--
-- Inputs (rendered by the Workflows-tab Run form, collected in chat when
-- triggered conversationally):
--   youtube_url (required text) · num_shorts (number, default 3) ·
--   aspect (enum 9:16|1:1|16:9, default 9:16)
--
-- Depends on the yt-dlp + ffmpeg CLI extensions (migration 142). Idempotent.

BEGIN;

INSERT INTO mem_workflows (name, description, steps, inputs, source, enabled)
VALUES (
  'download-transcribe-shorts',
  'Download a YouTube video, find the best moments from its transcript, and cut them into shorts. Fixed pipeline: download -> select -> cut -> you approve.',
  $steps$[
    {
      "name": "download",
      "kind": "agent",
      "spec": {
        "prompt": "Download the YouTube video at {{input.youtube_url}} into the cloud workspace using yt-dlp (source /workspace/.jarvis/env.sh first). Get BOTH the video file and the English transcript - prefer auto-captions (yt-dlp --write-auto-subs --sub-langs en --convert-subs srt); only if captions are missing, transcribe the audio. Report exactly: the absolute video file path, and the transcript (or its path) with timestamps. Do not pick segments yet."
      },
      "max_attempts": 2
    },
    {
      "name": "select-best",
      "kind": "agent",
      "spec": {
        "prompt": "From the transcript in the previous step, choose the {{input.num_shorts}} BEST moments to become standalone shorts. Each must be a self-contained, high-hook clip of 60 seconds or less - a strong opening line, a surprising claim, a punchy payoff. For each, output: start timestamp, end timestamp (<=60s span), and a short title. Output a clean numbered list only - this is the judgment step, be selective."
      },
      "max_attempts": 2
    },
    {
      "name": "cut-shorts",
      "kind": "agent",
      "spec": {
        "prompt": "For EACH segment chosen in the previous step, cut a short from the downloaded video with ffmpeg (source /workspace/.jarvis/env.sh first): trim to the segment, and for a vertical {{input.aspect}} target crop/scale accordingly (e.g. 9:16 -> crop=ih*9/16:ih,scale=1080:1920). Run each cut through the media_job tool (result=output_files, output_glob pointing at the produced file) so every short is tracked, saved to the Library, and shown in the Media tab. Report the list of produced shorts."
      },
      "max_attempts": 2
    },
    {
      "name": "approve",
      "kind": "checkpoint",
      "spec": {
        "message": "Review the shorts in the Media tab. Approve to finish, or tell me what to recut."
      }
    }
  ]$steps$::jsonb,
  $inputs$[
    {"key":"youtube_url","label":"YouTube URL","type":"text","required":true,"doc":"The video to pull shorts from."},
    {"key":"num_shorts","label":"How many shorts","type":"number","required":false,"default":"3"},
    {"key":"aspect","label":"Aspect","type":"enum","options":["9:16","1:1","16:9"],"required":false,"default":"9:16"}
  ]$inputs$::jsonb,
  'manual',
  TRUE
)
ON CONFLICT (name) DO UPDATE SET
  description = EXCLUDED.description,
  steps       = EXCLUDED.steps,
  inputs      = EXCLUDED.inputs,
  updated_at  = NOW();

COMMIT;
