-- 142_seed_media_clis.sql - seed yt-dlp + a static ffmpeg as cloud-resident
-- CLI extensions, so the media finishing/ingest tools self-install on the
-- workspace volume (no Docker redeploy, no Mac dependency).
--
-- Both are lightweight, no-auth installs that land on $HOME/.local/bin (pinned
-- to the Railway volume via env.sh), so LoadAll can activate them on boot
-- without the heavy first-run cost a torch-based whisper would carry. Whisper
-- is deliberately NOT seeded: the Motion Graphics doctrine is transcript-first
-- (yt-dlp --write-auto-subs), with whisper only as a fallback the agent can
-- provision on demand for the rare caption-less video.
--
-- Idempotent ON CONFLICT (name) DO NOTHING.

BEGIN;

-- yt-dlp - the maintained youtube-dl successor; canonical YouTube ingest.
INSERT INTO mem_extensions
  (name, kind, description, config, enabled, source, status)
VALUES (
  'yt-dlp',
  'cli',
  'Download YouTube (and 1000+ sites) video + auto-captions. Transcript-first ingest for the shorts pipeline.',
  $cfg${
    "install": "python3 -m pip install --user -U yt-dlp",
    "binary": "yt-dlp",
    "check_cmd": "yt-dlp --version",
    "auth_envs": [],
    "usage": "Download: yt-dlp -o \"%(title)s.%(ext)s\" <url> . Transcript-first (preferred): yt-dlp --skip-download --write-auto-subs --sub-langs en --convert-subs srt -o \"%(title)s.%(ext)s\" <url> . Metadata: yt-dlp --skip-download --dump-json <url> ."
  }$cfg$::jsonb,
  TRUE,
  'manual',
  'active'
)
ON CONFLICT (name) DO NOTHING;

-- ffmpeg - static build to the volume (no apt/root needed). The cut/encode
-- muscle for shorts + the design renderer's encode step.
INSERT INTO mem_extensions
  (name, kind, description, config, enabled, source, status)
VALUES (
  'ffmpeg',
  'cli',
  'Video/audio cut, crop, caption, encode (static build, cloud-resident). The finishing muscle for shorts and rendered graphics.',
  $cfg${
    "install": "set -e; mkdir -p \"$HOME/.local/bin\" /tmp/ffdl && cd /tmp/ffdl && curl -fsSL -o ff.tar.xz https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz && tar xJf ff.tar.xz && d=$(find . -maxdepth 1 -type d -name 'ffmpeg-*-static' | head -1) && cp \"$d/ffmpeg\" \"$d/ffprobe\" \"$HOME/.local/bin/\" && rm -rf /tmp/ffdl",
    "binary": "ffmpeg",
    "check_cmd": "ffmpeg -version",
    "auth_envs": [],
    "usage": "Cut a clip: ffmpeg -ss <start> -to <end> -i in.mp4 -c copy out.mp4 . Crop to vertical 9:16: ffmpeg -i in.mp4 -vf \"crop=ih*9/16:ih,scale=1080:1920\" out.mp4 . Always run finishing renders through media_job so the asset is tracked + surfaced."
  }$cfg$::jsonb,
  TRUE,
  'manual',
  'active'
)
ON CONFLICT (name) DO NOTHING;

COMMIT;
