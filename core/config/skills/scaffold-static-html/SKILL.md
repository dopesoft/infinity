---
name: scaffold-static-html
version: "1.0.0"
description: Scaffold a single-page static HTML/CSS/JS app served by a tiny dev server.
trigger_phrases:
  - static html
  - simple html page
  - vanilla js
  - no framework
  - just html and css
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
network_egress: none
confidence: 0.99
---

# Scaffold a static HTML/CSS/JS app

When the boss wants something tiny — a landing page, a calculator, a
data visualization — skip the build tooling entirely.

## Steps

1. Pick a slug. Create the directory under `$INFINITY_CANVAS_ROOT/<slug>/`.
2. Write three files inside:

   - `index.html` — boilerplate with `<link rel="stylesheet" href="style.css">` and `<script type="module" src="app.js" defer>`.
   - `style.css` — reset + the requested styling.
   - `app.js` — initial JS, even if empty (use `console.log("ready")`).

3. The supervisor serves the directory with a tiny static server on port
   **5050** (no install needed; the bridge has a built-in static handler
   for `scaffold-static-html` projects).
4. Attach via `POST /api/sessions/:id/project` with `project_template: "static-html"`, `dev_port: 5050`.

## Why this template

Zero install. Zero build. Boss sees the page in <1 second. Ideal for
"show me a button that does X" requests.

## Gotchas

- No bundler means relative paths matter — keep assets inside the project dir.
- The static server is read-only relative to `project_path` (no API routes).
  If the boss needs an API, propose escalating to Vite or Next.
