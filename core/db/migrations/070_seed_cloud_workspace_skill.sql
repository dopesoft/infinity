-- 070_seed_cloud_workspace_skill.sql
--
-- KEYSTONE skill: teach Jarvis to use his own LITERAL CLOUD COMPUTER and to
-- self-provision tooling — so he is a strong agent WITH OR WITHOUT the Mac.
--
-- The substrate already exists (verified):
--   • docker/workspace = an always-on Debian box on Railway with network,
--     python3/pip/pipx/venv, node/npm/pnpm, build-essential, git, ripgrep/jq,
--     and a full doc/OCR/PDF stack (libreoffice, poppler/pdftotext, tesseract,
--     qpdf, pdftk, ghostscript). `/workspace` is a PERSISTENT VOLUME that
--     survives deploys + App Sleeping (railway.toml).
--   • bridge.Router (core/internal/bridge) routes the generic primitives
--     fs_read/fs_save/fs_edit/bash_run/git_* to the Mac when it's healthy and
--     to this cloud workspace otherwise. `claude_code__*` are Mac-ONLY.
--   • Only filesystem-destructive bash (rm/dd/shred/…) gates; installs run
--     UNATTENDED (core/internal/proactive/gate.go + agent/gate.go).
--
-- What was missing was the COGNITION: that the cloud computer exists, which
-- tools reach it, and how to install tooling so it PERSISTS. That's a recipe,
-- not Go — so it ships as a skill (Rule #1 / Rule #1a). Other seeded skills
-- (069 coding, 072 docs) reference this one for provisioning + routing.
--
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'cloud-workspace',
  'Use Jarvis''s always-on cloud computer (the Railway workspace) to run real work with or without the Mac: a persistent Debian box with network, python/node/build tools, a doc/OCR/PDF stack, and a /workspace volume that survives restarts. Covers tool routing (fs_*/bash_run/git_* go Mac-or-cloud; claude_code__* is Mac-only) and how to self-install tooling so it PERSISTS. Use whenever a task needs a shell, a CLI/library that isn''t loaded, a place to build/run code, or must keep working while the Mac is offline.',
  'low',
  '["workspace:*"]'::jsonb,
  '["use the cloud","cloud computer","run this in the cloud","mac is off","without my computer","install a tool","pip install","npm install","apt install","need a CLI","set up tooling","run a script","build environment","persistent workspace","self provision"]'::jsonb,
  '[{"name":"task","type":"string","doc":"what needs a shell / tooling / a place to run"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"the outcome + where any artifacts landed in /workspace"}]'::jsonb,
  0.9, 'active', 'hermes_imported', 88,
  'Foundational: makes Jarvis a strong autonomous agent that works with OR without the Mac, and can self-provision its own tooling durably in the cloud.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'cloud-workspace', 'v1.0-5-24-2026',
  $skill$---
name: cloud-workspace
version: "v1.0-5-24-2026"
description: Use the always-on cloud computer (Railway workspace) to run real work with or without the Mac — persistent /workspace volume, network, python/node/build tools, doc/OCR/PDF stack. Covers tool routing and durable self-provisioning.
trigger_phrases:
  - use the cloud
  - cloud computer
  - run this in the cloud
  - mac is off
  - without my computer
  - install a tool
  - pip install
  - npm install
  - need a CLI
  - set up tooling
inputs:
  - name: task
    type: string
outputs:
  - name: result
    type: string
risk_level: low
network_egress: workspace
confidence: 0.9
---

# Your cloud computer

You are not dependent on the boss's Mac. You have your own **always-on Linux
computer in the cloud** — the Railway `workspace` service. It is a real Debian
box with outbound network, a shell, and a **persistent `/workspace` volume** that
survives deploys, restarts, and App Sleeping. This is what makes you a strong
agent: you work **with or without a computer of the boss's online.**

## Which tools reach which machine

Your file/shell/git verbs route automatically through the bridge Router:

- **`fs_read`, `fs_save`, `fs_edit`, `fs_ls`, `bash_run`, `git_status`, `git_diff`,
  `git_stage`, `git_commit`, `git_push`, `git_pull`** — route to the **Mac when it's
  online**, and to the **cloud workspace** when it isn't. Use THESE for anything that
  must run unattended or while the Mac is asleep.
- **`claude_code__*`** (Bash/Edit/Read/Grep/…) — **Mac-ONLY** (the home Mac over the
  Cloudflare tunnel). Great when the boss is at his desk and the work touches his
  local repos/files. Useless when the Mac is off — don't reach for them for
  always-on / cron / heartbeat work.

When in doubt, or when the task must not depend on the Mac, **use the bridge
primitives (`bash_run`, `fs_*`, `git_*`)**.

## What's already installed (don't reinstall these)

The cloud box ships with: `git`, `curl`, `ripgrep` (`rg`), `jq`, `python3` +
`pip`/`pipx`/`venv`, `node`/`npm`/`pnpm`, `build-essential` (gcc/make), and a full
document/OCR stack — **LibreOffice headless, `pdftotext`/`pdfimages` (poppler),
`tesseract` OCR, `qpdf`, `pdftk`, `ghostscript`**. Check before installing:
`command -v <tool>`.

## Self-provisioning — install what you need

You can install your own tooling. Installs run **unattended** — only
filesystem-destructive commands (`rm`/`dd`/`shred`/…) ever stop for approval, so
`pip install`, `npm i -g`, `apt-get install`, `pipx install`, `curl … | sh`,
downloading a binary — all just run. Heavy toolchains (Go, Rust, Bun) are meant to
be installed on first use.

### Make it PERSIST (this is the important part)

The container's **system filesystem is ephemeral** — anything `apt-get install`
puts in `/usr/bin` is gone on the next redeploy. Only **`/workspace` persists.**
So install durably:

- **Python:** make a venv inside the volume — `python3 -m venv /workspace/.venv &&
  /workspace/.venv/bin/pip install <pkg>`. Call tools via `/workspace/.venv/bin/…`.
- **Node:** install into a project under `/workspace` (`npm install` there), or set a
  volume-backed prefix; global `npm i -g` is not durable across redeploys.
- **Binaries / CLIs:** download into `/workspace/bin` and add it to PATH
  (`export PATH=/workspace/bin:$PATH`).
- **Bootstrap script:** keep a `/workspace/.bootstrap.sh` that (re)installs the tools
  you rely on. If a tool is unexpectedly missing after a cold start, run it. Append a
  new install line whenever you provision something you'll want again.
- `apt-get install <x>` is fine for a one-off in the current session; if you'll need
  it repeatedly, add it to `.bootstrap.sh` (and if it's universal, the boss can bake
  it into `docker/workspace/Dockerfile` for a permanent fix).

If an install needs system libs you can't get without root persistence, say so and
suggest the Dockerfile bake — don't silently limp.

## Where artifacts live

Write outputs under `/workspace` (e.g. `/workspace/projects/<name>`,
`/workspace/out`). Register anything the boss should see with `artifact_save` so it
shows up in his library. The volume is the durable home; `/tmp` is scratch.

## Choosing Mac vs cloud

- **Cloud workspace** — default for autonomous/unattended work, cron + heartbeat
  jobs, building/running code that doesn't need the boss's local files, anything that
  must keep working while he's away. Always on.
- **Mac (`claude_code__*` or Router-on-Mac)** — when the work is on the boss's local
  repos/files, or needs something only the Mac has (his apps, local network, iMessage,
  signed-in CLIs). Only available when the Mac is online.

## Hard rules

- Destructive filesystem ops still gate for Trust approval — that's intended.
- Don't write secrets/API keys into `/workspace` in plaintext; use the env the
  service already holds. Treat the volume like a shared disk.
- Don't `git push` from the cloud unless the task is explicitly a cloud-side commit;
  the boss owns git on his Mac.
- Keep `/workspace` tidy — it's persistent, so junk accumulates. Clean scratch dirs
  when done.
$skill$,
  '', 0.9, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('cloud-workspace', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
