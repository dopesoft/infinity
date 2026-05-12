---
name: scaffold-nextjs
version: "1.0.0"
description: Scaffold a new Next.js app under the session's project root and attach it.
trigger_phrases:
  - build me a next.js app
  - create next.js app
  - new nextjs project
  - scaffold next
  - make a next app
inputs:
  - name: slug
    type: string
    required: true
    doc: kebab-case directory name to create under INFINITY_CANVAS_ROOT
outputs:
  - name: project_path
    type: string
    doc: absolute path where the scaffold landed
  - name: dev_port
    type: int
    doc: 3000 by default
risk_level: high
network_egress:
  - registry.npmjs.org
  - github.com
confidence: 0.9
---

# Scaffold a Next.js app

You are creating a new Next.js 14 app for the boss inside their workspace.

## Steps

1. Pick a kebab-case slug from the boss's request (e.g. "chat-app", "task-tracker"). Keep it short.
2. Run from `$INFINITY_CANVAS_ROOT` (default `/Users/n0m4d/Dev`):

   ```bash
   pnpx create-next-app@latest <slug> \
     --typescript --tailwind --eslint --app \
     --src-dir --import-alias "@/*" --no-turbo \
     --use-pnpm
   ```

3. Tell the boss the project path and that the dev server runs on port **3000** with `pnpm dev`. The Canvas supervisor will pick it up automatically.
4. Attach the project to the current session via `POST /api/sessions/:id/project` with `project_path`, `project_template: "nextjs"`, `dev_port: 3000`.

## What good looks like

- App boots at `http://localhost:3000` via `pnpm dev`.
- Boss sees the default Next page in the Canvas preview pane within ~10s.
- Subsequent edits hot-reload without intervention.

## Gotchas

- `create-next-app` is interactive without explicit flags. Always pass all of them.
- If pnpm isn't installed, fall back to `npx create-next-app@latest` and `npm run dev`.
- Don't pick a slug that already exists under `$INFINITY_CANVAS_ROOT` — append `-2`, `-3`, etc.
