-- 165_ytdlp_botwall_recipe.sql - teach the yt-dlp extension the bot-wall
-- recovery recipe, in its usage line (injected into the system prompt every
-- turn by extensions.CLIProvider).
--
-- 2026-07-09: the boss asked for a YouTube transcript. yt-dlp ran correctly on
-- the cloud workspace and YouTube refused it: "Sign in to confirm you're not a
-- bot. Use --cookies-from-browser or --cookies" - datacenter egress IPs are
-- bot-walled for anonymous traffic. The usage line had no recipe for that
-- failure, so the agent improvised: browser thrash, then third-party
-- "transcript generator" sites. The right move was in yt-dlp's own error
-- message the whole time: pass cookies.
--
-- The standing mechanism (matches the web-reach cookie hand-off in 155): a
-- ONE-TIME cookie export from the boss's logged-in browser, saved on the
-- persistent volume as /workspace/.jarvis/cookies/youtube.txt (Netscape
-- format; the env.sh update creates the dir). This line carries the judgment:
-- when to use it, how to ask for it, and what NOT to do (no third-party
-- scraper sites, no blind retries).
--
-- Data-only; works with the currently-deployed core. Idempotent (plain SET).

BEGIN;

UPDATE mem_extensions
   SET config = jsonb_set(
         config,
         '{usage}',
         to_jsonb(
           'Download: yt-dlp -o "%(title)s.%(ext)s" <url> . ' ||
           'Transcript-first (preferred): yt-dlp --skip-download --write-auto-subs --sub-langs en --convert-subs srt -o "%(title)s.%(ext)s" <url> . ' ||
           'Metadata: yt-dlp --skip-download --dump-json <url> . ' ||
           'IF YOUTUBE SAYS "Sign in to confirm you''re not a bot": the cloud IP is bot-walled, not broken. ' ||
           'Retry once adding: --cookies /workspace/.jarvis/cookies/youtube.txt . ' ||
           'If that file is missing, ask the boss (plain words): "YouTube wants proof I''m not a bot - export your YouTube cookies once (Cookie-Editor extension, Netscape format) and paste them here; it lasts months." ' ||
           'Save exactly what he pastes to that path, then retry. ' ||
           'NEVER use third-party transcript/downloader websites and NEVER retry the same blocked call without cookies.'
         )
       ),
       updated_at = NOW()
 WHERE name = 'yt-dlp' AND kind = 'cli';

COMMIT;
