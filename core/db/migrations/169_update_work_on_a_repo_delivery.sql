-- 169_update_work_on_a_repo_delivery.sql
--
-- Close the autonomous-delivery loop in the work-on-a-repo recipe. v1.0
-- stopped at "create/clone + install deps" — a build kicked off from the
-- boss's phone produced code with no delivery moment: nothing booted the
-- preview (that was Studio-UI-only until the preview_start tool), nothing
-- verified the app actually renders, and nothing handed the boss a link.
--
-- The mechanics shipped in code alongside this migration:
--   • preview_start / preview_stop / preview_status tools (agent-callable
--     supervisor control; preview_start returns the tappable URL)
--   • the nextjs scaffold now writes a basePath-aware next.config.mjs so
--     dev previews render correctly behind the /api/canvas/preview proxy
--   • notify({conversation:true}) opens a session the push deep-links into
--
-- v2.0 adds only the JUDGMENT: define done up front (mandate), build, boot
-- the preview, verify before claiming, and deliver a link + narrative -
-- in-chat when the boss is present, as a conversational push when not.
--
-- Ships a new version and repoints mem_skill_active (same pattern as 160).

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'work-on-a-repo',
  'v2.0-7-10-2026',
  $skill$---
name: work-on-a-repo
version: "v2.0-7-10-2026"
description: Create a new app or clone an existing repo into the projects store (separate from Jarvis's own code), build what was asked, boot the live preview, verify it, and deliver the boss a tappable link + narrative. Works on cloud or Mac.
trigger_phrases:
  - clone this repo
  - work on this repo
  - let's work on a project
  - start a new project
  - build a new app
  - build me a
  - make me an app
  - clone and run
inputs:
  - name: intent
    type: string
outputs:
  - name: project_path
    type: string
  - name: preview_url
    type: string
  - name: summary
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.9
---

# Work on a repo

You have a real, self-contained computer (see `cloud-workspace`). Its disk is laid
out so **your own code and the boss's projects never mix**:

- **`/workspace/infinity`** — *your* code. You edit here only to fix/improve yourself.
- **`/workspace/projects/<name>`** — each app the boss builds, its **own git repo**
  with its own GitHub remote. This is where project work happens.

(On the Mac it's the Mac infinity repo + `~/Dev/projects/<name>` — same idea.)

Never scaffold or clone an app into `/workspace/infinity` or the infinity repo — that
pollutes your self-code. Always use the projects store.

## Define done first

When the ask is "build me X", open a mandate (`mandate_open`) before writing code,
with 2-4 binary criteria from the boss's words — e.g. "landing page renders with
hero + pricing", "form submits without errors", "preview URL responds". This is
your definition of done; you'll verify against it before delivering.

## Decide: create or clone

- **"Build a new <app>"** → `project_create` (template scaffold + `git init` + initial
  commit + optional GitHub repo). Pick the template from the boss's stack
  (nextjs / vite-react / static-html / go / python / empty).
- **"Clone <repo url> and let's work on it"** → `project_clone` with the URL. It clones
  into the projects store, sets the session's project so the canvas scopes to it, and
  registers the project so it shows in the switcher.

Both tools do the bookkeeping for you (directory, git, the project registry, the
session's `project_path`). Don't hand-roll `git clone` via bash when these exist — they
keep the project enumerable in the switcher.

## Build

1. **Open + orient.** The canvas re-scopes to the new project automatically (its
   `project_path` is set). Read the README / package manifest to learn the stack.
2. **Install dependencies.** Run the project's installer with `bash_run` *inside the
   project dir* — `npm install` / `pnpm install` / `pip install -r requirements.txt` /
   `go mod download`. It persists on the volume (see `cloud-workspace`). Detect the
   manifest; don't guess.
3. **Do the actual work** — build what was asked using your file + bash tools
   (they're already scoped to this project's bridge). Edits stream into the canvas.

## Deliver — the boss gets a link, not a status

Building without delivering is half the job. When the work is done:

1. `preview_start` — boots the dev server on the project's bridge and returns the
   URL. Then `preview_status` until the record reports the server is up; if it
   won't come up, that's a bug to fix now, not a caveat to ship.
2. **Verify against the mandate.** Check each criterion honestly
   (`mandate_check` with evidence — what you saw, not what you intended), then
   `mandate_close`. If a criterion fails, keep working; never deliver a link to
   something you haven't seen respond.
3. **Register the deliverable:** `surface_item` with `surface: "deliveries"`,
   `kind: "deliverable"`, `external_id: "project:<slug>"`, title "<App name> — live",
   the preview URL as `url`, and a short plain-English narrative of what you built
   and any choices you made.
4. **Hand it over.** If the boss is in this chat, reply with the link + narrative
   directly. If this run is autonomous (cron, background, he asked from his phone
   and left), `notify` with `conversation: true` — "Your <app> is built and live —
   <url>. I made <choice>; want anything adjusted?" — so the push lands him in a
   chat where he can react.

## Git + GitHub

- The project has its **own** `origin` remote. Commit with clear messages; push/pull
  against *its* remote (not infinity's). The canvas Changes tab + per-project
  ahead/behind reflect this repo, not your self-code.
- For a brand-new project, `project_create` can create the GitHub repo and push the
  first commit. For a clone, the remote already exists.
- Git history/pushes are the boss's domain — commit freely, but surface before force
  operations.

## Keep self and projects straight

- If the boss asks you to change how *you* work (your behavior, your tools, your
  prompts), that's the **infinity** repo (`/workspace/infinity`) — a different context.
- If it's "their app", it's a **project**. When in doubt, ask which.
- The canvas breadcrumb shows where you are ("Jarvis (self)" vs "Project: <name>") —
  glance at it before you start editing so you don't commit app code into your self-repo.
$skill$,
  '', 0.9, 'boss_requested'
)
ON CONFLICT (skill_name, version) DO NOTHING;

UPDATE mem_skill_active
   SET active_version = 'v2.0-7-10-2026'
 WHERE skill_name = 'work-on-a-repo';

COMMIT;
