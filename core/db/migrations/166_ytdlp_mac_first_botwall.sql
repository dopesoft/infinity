-- 166_ytdlp_mac_first_botwall.sql - the bot-wall ladder, Mac first.
--
-- 165 taught the cookie recovery. Then the experiment (2026-07-09): the
-- IDENTICAL yt-dlp transcript command that YouTube bot-walled on the cloud
-- workspace succeeded instantly from the boss's Mac - same stale binary
-- (brew, /opt/homebrew/bin), same video, different IP. The datacenter IP was
-- the whole problem. Commercial "always works" transcript services solve this
-- by RENTING residential IPs; the Mac bridge IS a residential IP we already
-- own.
--
-- So the recovery ladder becomes:
--   1. Mac bridge (zero boss effort, verified working)   - bash_run bridge:"mac"
--   2. Cookie file on the cloud (works when Mac asleep)  - --cookies ...
--   3. Ask the boss for the one-time cookie export
-- and never: third-party scraper sites, blind retries.
--
-- Data-only; the deployed bash_run already honors the "bridge" argument.
-- Idempotent (plain SET).

BEGIN;

UPDATE mem_extensions
   SET config = jsonb_set(
         config,
         '{usage}',
         to_jsonb(
           'Download: yt-dlp -o "%(title)s.%(ext)s" <url> . ' ||
           'Transcript-first (preferred): yt-dlp --skip-download --write-auto-subs --sub-langs en --convert-subs srt -o "%(title)s.%(ext)s" <url> . ' ||
           'Metadata: yt-dlp --skip-download --dump-json <url> . ' ||
           'IF YOUTUBE SAYS "Sign in to confirm you''re not a bot": the cloud IP is bot-walled, not broken. Recovery ladder, in order: ' ||
           '(1) Rerun the SAME command via bash_run with "bridge":"mac" and the command prefixed PATH="/opt/homebrew/bin:$PATH" - yt-dlp is installed on the Mac and its home IP is not bot-walled (verified 2026-07-09). ' ||
           '(2) Mac bridge down? Retry on cloud adding: --cookies /workspace/.jarvis/cookies/youtube.txt . ' ||
           '(3) No cookie file? Ask the boss (plain words): "YouTube wants proof I''m not a bot - export your YouTube cookies once (Cookie-Editor extension, Netscape format) and paste them here; it lasts months." Save exactly what he pastes to that path, then retry. ' ||
           'NEVER use third-party transcript/downloader websites and NEVER retry the same blocked call unchanged.'
         )
       ),
       updated_at = NOW()
 WHERE name = 'yt-dlp' AND kind = 'cli';

COMMIT;
