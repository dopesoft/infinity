-- 074_seed_work_on_a_repo_skill.sql
--
-- The recipe for "let's work on a project" — create a new app OR clone an
-- existing repo, on Jarvis's cloud computer (or the Mac), as a SELF-CONTAINED
-- git repo kept SEPARATE from Jarvis's own infinity code.
--
-- Rule #1: the deterministic substrate is Go (project_create / project_clone
-- tools do the scaffold/clone + mem_artifacts registry + session project_path
-- bookkeeping; bridge.Router routes to the right machine). This skill carries
-- the JUDGMENT — when to create vs clone, where things live, install deps,
-- open + orient, keep self and projects apart.
--
-- Disk layout (cloud): /workspace is the disk →
--   /workspace/infinity        Jarvis's own code (fix himself) — NOT for apps
--   /workspace/projects/<name> each app, its own git repo + GitHub remote
-- Mac: the Mac infinity repo + ~/Dev/projects/<name>.
--
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'work-on-a-repo',
  'Start or resume work on a software project on Jarvis''s cloud computer (or the Mac): create a new app OR clone an existing git repo into the projects store (separate from Jarvis''s own infinity code), open it in the canvas, install its dependencies, and keep it synced with GitHub. Use when the boss says "let''s build/clone/work on <repo or app>".',
  'medium',
  '["composio:github","workspace:*"]'::jsonb,
  '["clone this repo","work on this repo","let''s work on a project","start a new project","build a new app","open the project","clone and run","set up this repo","pull this repo and","let''s build"]'::jsonb,
  '[{"name":"intent","type":"string","doc":"create a new project, or clone+work on an existing repo (url)"}]'::jsonb,
  '[{"name":"project_path","type":"string","doc":"where the project lives on the active bridge"},{"name":"summary","type":"string","doc":"what was set up + how to run it"}]'::jsonb,
  0.88, 'active', 'hermes_imported', 78,
  'Core "self-contained dev computer" loop: spin up / clone real git projects separate from Jarvis''s own code, on whichever bridge.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'work-on-a-repo', 'v1.0-5-24-2026',
  $skill$---
name: work-on-a-repo
version: "v1.0-5-24-2026"
description: Create a new app or clone an existing repo into the projects store (separate from Jarvis's own code), open it, install deps, keep it synced with GitHub. Works on cloud or Mac.
trigger_phrases:
  - clone this repo
  - work on this repo
  - let's work on a project
  - start a new project
  - build a new app
  - clone and run
inputs:
  - name: intent
    type: string
outputs:
  - name: project_path
    type: string
  - name: summary
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.88
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

## After create/clone

1. **Open + orient.** The canvas re-scopes to the new project automatically (its
   `project_path` is set). Read the README / package manifest to learn the stack.
2. **Install dependencies.** Run the project's installer with `bash_run` *inside the
   project dir* — `npm install` / `pnpm install` / `pip install -r requirements.txt` /
   `go mod download`. It persists on the volume (see `cloud-workspace`). Detect the
   manifest; don't guess.
3. **Run it** if asked — start the dev server / build; report the URL or output.
4. **Work the task** using your file + bash tools (they're already scoped to this
   project's bridge). Edits stream into the canvas; the boss watches.

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
  '', 0.88, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('work-on-a-repo', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
