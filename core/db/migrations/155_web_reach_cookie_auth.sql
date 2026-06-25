-- 155_web_reach_cookie_auth.sql - correct the web-reach skill's gated-platform
-- recipe.
--
-- 154 shipped web-reach v1.0.0, whose "login-gated platforms" section wrongly
-- described a device sign-in URL flow (that's how device-OAuth CLIs like
-- higgsfield work). Twitter/X and Reddit are COOKIE-based: the tool borrows the
-- boss's logged-in browser session. The cloud box has no such browser, so the
-- real one-time setup is a cookie hand-off (export a Cookie-Editor "Header
-- String", paste it, Jarvis applies it cloud-side via the command `agent-reach
-- doctor` names for that channel). LinkedIn genuinely needs an interactive
-- Chromium login with a display - heavier, called out honestly, not papered over
-- as a cookie paste.
--
-- Ships as v1.1.0 (full corrected body) and repoints mem_skill_active so
-- MaterializeActiveSkills derives it on the next boot. Idempotent: the version
-- INSERT is ON CONFLICT DO NOTHING; the active pointer is DO UPDATE so the bump
-- actually takes (DO NOTHING would leave it stuck on 1.0.0).

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'web-reach',
  '1.1.0',
  $skill$---
name: web-reach
version: "1.1.0"
description: Research the internet without paywalls via the agent-reach CLI - read any web page, YouTube transcript, article, RSS item, run a web search, or (with a one-time cookie sign-in) Twitter/X and Reddit. Pick the right command; the tool scrapes and falls back.
trigger_phrases:
  - research this
  - find on the web
  - look this up
  - search the web for
  - gather data on
  - pull this article
  - read this page
  - get the transcript
  - summarize this video
  - without the paywall
  - find information about
  - search twitter
  - what are people saying
  - check reddit
inputs:
  - name: need
    type: string
    required: false
    doc: what to find or read - usually already the boss's message in this conversation
outputs:
  - name: content
    type: object
    doc: the fetched text/result and its source URL(s)
risk_level: low
network_egress:
  - r.jina.ai
  - api.exa.ai
  - youtube.com
  - github.com
  - x.com
  - reddit.com
confidence: 0.85
---

# Reach the internet without paywalls (agent-reach)

The boss wants something off the internet — an article behind a soft paywall, a
YouTube video's transcript, a GitHub README, what people are saying on Twitter or
Reddit, or just an open-ended "find me X". `agent-reach` is a router over
open-source scrapers that reads these without per-service API keys. Your job is
judgment: pick the right command for the need, then read what comes back.

## How to run it (it lives on the cloud workspace)

`agent-reach` is cloud-resident, so run it through `bash_run` pinned to the cloud
box — that's where the tool and its credentials live, no matter which bridge the
boss is on:

    bash_run(
      bridge: "cloud",
      cmd: "source /workspace/.jarvis/env.sh && agent-reach <subcommand> ..."
    )

Always source `env.sh` first (it puts the install on PATH). Always pass
`bridge:"cloud"` — on a Mac session a bare command would miss the tool.

## Which command for which need

- **Read one URL → clean text.** Any web page, article, YouTube link (returns the
  transcript), RSS feed, GitHub page, or a specific tweet/Reddit thread URL:
  `agent-reach read "<url>"`. Your default for "what does this say", "summarize
  this", "get the transcript".
- **Open-ended search → find pages.** When the boss doesn't have a URL:
  `agent-reach search "<query>"`, then `read` the promising results.
- **Twitter / Reddit search** once signed in (below): use agent-reach's
  platform read/search for that URL or handle. If unsure of the exact subcommand,
  `agent-reach doctor` and the channel's own help print the current syntax.
- **Not sure it's working?** `agent-reach doctor` reports which channels are green
  and how to fix a red one. Run it if a read/search comes back empty — don't
  assume the page was empty when the channel might just be down.

## Signing in to Twitter/X and Reddit (cookie hand-off)

Twitter and Reddit need a logged-in session. They do NOT use a device sign-in URL
— they borrow the boss's **browser cookies**. The cloud box has no such browser,
so setup is a one-time cookie hand-off:

1. Run `agent-reach doctor` (bridge:"cloud") and read the per-channel setup line
   it prints for `twitter` / `reddit` — that's the authoritative command + syntax
   for this build of the tool. Follow it; don't hardcode flags from memory. (It
   may first need the channel installed, e.g. `agent-reach install --channels=twitter`.)
2. The boss hands over his session by exporting a "Header String" from the
   **Cookie-Editor** browser extension while logged into x.com / reddit.com. When
   he pastes it, apply it cloud-side with the command doctor named (e.g.
   `agent-reach configure twitter-cookies "<string>"`), then verify with a real
   read. Write it on the cloud box only; never echo the cookie string back into
   the chat.
3. His real account works; a dedicated throwaway lowers ban risk — his call, not a
   requirement. Cookies expire: when reads start failing, ask him to re-export and
   paste again.

**LinkedIn** is heavier — it needs an interactive Chromium login with a display on
the cloud box, not a cookie paste. If the boss asks for LinkedIn, say it's a
separate setup step (a virtual display on the workspace + a browser login streamed
to his Preview pane) rather than pretending a paste covers it.

## Report

Give the boss what he actually wanted — the answer or the content — with the
source link, in plain language. If a read came back empty, say so and what you
tried (and run `doctor`) rather than inventing content.

## Hard rules

- Run agent-reach via `bash_run(bridge:"cloud", …)` with `env.sh` sourced — never
  assume it's on the Mac.
- Never run a raw `<tool> login` in bash, and never drive a platform sign-in on the
  boss's own Mac browser. Cookie config is applied on the cloud box.
- Never echo a pasted cookie/credential back into the chat.
- Never fabricate fetched content. Empty result → report it and check `doctor`.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

UPDATE mem_skill_active
   SET active_version = '1.1.0', updated_at = NOW()
 WHERE skill_name = 'web-reach';

UPDATE mem_skills
   SET description = 'Research and gather data from the internet without paywalls: read any web page, pull a YouTube transcript, fetch an article or RSS item, run a web search, or (with a one-time cookie sign-in) search Twitter/X and Reddit - via the agent-reach CLI.',
       updated_at = NOW()
 WHERE name = 'web-reach';

COMMIT;
