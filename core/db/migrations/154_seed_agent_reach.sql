-- 154_seed_agent_reach.sql - paywall-free internet research, two pieces:
--
--   1. The BUILDING BLOCK: agent-reach as a cloud-resident kind=cli extension.
--      agent-reach (github.com/Panniantong/Agent-Reach) is itself a router over
--      open-source scrapers - Jina Reader (any web page -> markdown), yt-dlp
--      (YouTube captions), gh (GitHub), feedparser (RSS), Exa (web search) - so
--      the agent reads the internet without per-service API keys or paywalls.
--      It installs onto the persistent workspace volume via env.sh (HOME pinned
--      to /workspace/.jarvis), exactly like yt-dlp/ffmpeg in 142. The free set
--      (web, YouTube, RSS, GitHub, Exa search) needs NO sign-in, so status seeds
--      'active' and LoadAll installs + activates it on the next boot. The login-
--      gated platforms (Twitter/Reddit/LinkedIn) are deliberately NOT enabled
--      here - they need a dedicated throwaway account and ride the existing
--      extension_activate -> browser_open -> ExtensionAuthChecklist flow later.
--
--   2. The COGNITION: the web-reach skill (judgment-only, Rule #1b). WHICH
--      agent-reach subcommand serves WHICH research need, and the account-risk
--      judgment - nothing mechanical. The mechanics (install, cloud-pinning,
--      fallback routing) live in the extension/CLI/code, not this prose.
--
-- Idempotent: ON CONFLICT DO NOTHING never clobbers an evolved row.

BEGIN;

-- ── 1. The agent-reach CLI extension (cloud-resident, no auth for the free set) ──
--
-- install: pip-install the package onto the volume (--user lands the
-- `agent-reach` entrypoint in $HOME/.local/bin, on PATH via env.sh), then
-- best-effort provision the channel CLIs (yt-dlp/gh/Exa MCP) via the package's
-- own installer. The second step is bounded by `timeout` and `|| true` and reads
-- from /dev/null so it can NEVER hang or fail the activation - the binary is
-- already present from pip, and readiness = binary on PATH (empty check_cmd ->
-- cliReady falls back to `command -v agent-reach`).
INSERT INTO mem_extensions
  (name, kind, description, config, enabled, source, status)
VALUES (
  'agent-reach',
  'cli',
  'Paywall-free internet research: read any web page (Jina Reader), YouTube transcripts (yt-dlp), RSS, GitHub, and semantic web search (Exa) - no per-service API keys. A router over open-source scrapers; cloud-resident, reachable from any bridge.',
  $cfg${
    "install": "python3 -m pip install --user -U 'https://github.com/Panniantong/Agent-Reach/archive/refs/heads/main.zip' && { timeout 300 agent-reach install --env=auto </dev/null >/tmp/agent-reach-install.log 2>&1 || true; }",
    "binary": "agent-reach",
    "auth_envs": [],
    "usage": "Cloud-resident - run via bash_run with bridge=\"cloud\" and prefix `source /workspace/.jarvis/env.sh && `. Read any URL (web page, YouTube, RSS, GitHub) as clean text: `agent-reach read <url>`. Semantic web search: `agent-reach search \"<query>\"`. Health of each channel: `agent-reach doctor` (run it if a read returns nothing). The free set (web/YouTube/RSS/GitHub/Exa) needs no sign-in."
  }$cfg$::jsonb,
  TRUE,
  'manual',
  'active'
)
ON CONFLICT (name) DO NOTHING;

-- ── 2. The web-reach skill (judgment-only recipe) ──

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'web-reach',
  'Research and gather data from the internet without paywalls: read any web page, pull a YouTube transcript, fetch an article or RSS item, or run a web search - via the agent-reach CLI. Pick the right command for the need; the tool handles the scraping and fallback.',
  'low',
  '["r.jina.ai","api.exa.ai","youtube.com","github.com"]'::jsonb,
  '["research this","find on the web","look this up","search the web for","gather data on","pull this article","read this page","get the transcript","summarize this video","what does this page say","without the paywall","find information about","dig up","scrape this"]'::jsonb,
  '[{"name":"need","type":"string","required":false,"doc":"what to find or read - usually already the boss''s message in this conversation"}]'::jsonb,
  '[{"name":"content","type":"object","doc":"the fetched text/result and its source URL(s)"}]'::jsonb,
  0.85,
  'active',
  'manual',
  80,
  'Paywall-free research and data gathering is a foundational capability the boss explicitly asked for; it must work the first time he asks, on any bridge.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'web-reach',
  '1.0.0',
  $skill$---
name: web-reach
version: "1.0.0"
description: Research the internet without paywalls via the agent-reach CLI - read any web page, YouTube transcript, article, RSS item, or run a web search. Pick the right command; the tool scrapes and falls back.
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
confidence: 0.85
---

# Reach the internet without paywalls (agent-reach)

The boss wants something off the internet — an article behind a soft paywall, a
YouTube video's transcript, a GitHub README, what people are saying somewhere, or
just an open-ended "find me X". `agent-reach` is a router over open-source
scrapers that reads these without per-service API keys. Your job is judgment:
pick the right command for the need, then read what comes back.

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
  transcript), RSS feed, or GitHub page:
  `agent-reach read "<url>"`. This is your default for "what does this say",
  "summarize this", "get the transcript". It strips the paywall/chrome and gives
  you readable markdown.
- **Open-ended search → find pages.** When the boss doesn't have a URL ("find
  research on X", "who's writing about Y"): `agent-reach search "<query>"`. Then
  `read` the most promising result URLs for the full text.
- **Not sure it's working?** `agent-reach doctor` reports which channels are green
  and how to fix a red one. Run it if a `read`/`search` comes back empty — don't
  assume the page was empty when the channel might just be down.

## Login-gated platforms (Twitter/X, Reddit, LinkedIn)

These are NOT set up yet — they need a dedicated **throwaway** account's cookies,
never the boss's main account (cookie scraping is against their ToS and risks a
ban). If the boss asks for one of these, say it needs a one-time sign-in with a
throwaway account and offer to set it up via the self-contained flow
(register/activate the platform's CLI as an extension → it returns a sign-in URL
→ open it in your own cloud browser). Never sign in with his real account, and
never run a raw `<tool> login` in bash.

## Report

Give the boss what he actually wanted — the answer or the content — with the
source link, in plain language. If a read came back empty, say so and what you
tried (and run `doctor` to check the channel) rather than inventing content.

## Hard rules

- Run agent-reach via `bash_run(bridge:"cloud", …)` with `env.sh` sourced — never
  assume it's on the Mac.
- Never use the boss's real Twitter/Reddit/LinkedIn account for scraping; gated
  platforms are throwaway-only and opt-in.
- Never fabricate fetched content. Empty result → report it and check `doctor`,
  don't paper over it.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('web-reach', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
