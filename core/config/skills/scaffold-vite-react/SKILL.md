---
name: scaffold-vite-react
version: "1.0.0"
description: Scaffold a Vite + React + TypeScript app and attach it to the current session.
trigger_phrases:
  - build me a vite app
  - new vite project
  - scaffold vite
  - vite react
  - quick react app
inputs:
  - name: slug
    type: string
    required: true
outputs:
  - name: project_path
    type: string
  - name: dev_port
    type: int
risk_level: high
network_egress:
  - registry.npmjs.org
confidence: 0.95
---

# Scaffold a Vite + React + TypeScript app

Fast scaffold for a single-page React app. Better than Next.js for prototypes
that don't need SSR or routing.

## Steps

1. Pick a short kebab-case slug.
2. From `$INFINITY_CANVAS_ROOT`:

   ```bash
   pnpm create vite@latest <slug> -- --template react-ts
   cd <slug>
   pnpm install
   ```

3. Dev server runs on port **5173** via `pnpm dev`.
4. Attach via `POST /api/sessions/:id/project` with `project_template: "vite-react"`, `dev_port: 5173`.

## Why this template

Vite's HMR is faster than Next.js's; for a chat UI, a paint app, a small
dashboard, the boss usually wants this over Next unless they explicitly
need server components or SSR.

## Gotchas

- Default Vite dev binds to localhost only. If the Canvas preview proxy
  expects 127.0.0.1, this works as-is.
- Tailwind isn't included — if the boss asks for Tailwind, follow up with
  `pnpm add -D tailwindcss postcss autoprefixer && pnpm dlx tailwindcss init -p`
  and edit `vite.config.ts` / `src/index.css`.
