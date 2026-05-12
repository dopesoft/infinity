---
name: scaffold-capacitor
version: "1.0.0"
description: Wrap a web app in Capacitor for iOS/Android deployment.
trigger_phrases:
  - capacitor app
  - mobile wrapper
  - port to ios
  - port to android
  - wrap in capacitor
  - convert to capacitor
inputs:
  - name: base_template
    type: string
    default: vite-react
    doc: which web framework to scaffold first (nextjs | vite-react | static-html)
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
  - github.com
confidence: 0.7
---

# Scaffold a Capacitor app

For when the boss wants the same React/HTML app to run as a native
iOS/Android wrapper. Capacitor handles the bridge.

## Steps

1. First scaffold the underlying web app per `base_template` (default
   `vite-react`). Reuse the scaffold-vite-react / scaffold-nextjs /
   scaffold-static-html skills above; don't reimplement.
2. Add Capacitor:

   ```bash
   cd <slug>
   pnpm add @capacitor/core @capacitor/cli @capacitor/ios @capacitor/android
   pnpm dlx cap init "<slug>" "com.dopesoft.<slug>"
   pnpm dlx cap add ios
   pnpm dlx cap add android
   ```

3. Build the web app (`pnpm build`) and sync into the native shells:

   ```bash
   pnpm dlx cap sync
   ```

4. Canvas preview still shows the web app (same dev_port as base template)
   — Capacitor only matters when packaging for device.
5. Attach via `POST /api/sessions/:id/project` with `project_template: "capacitor-<base>"`.

## When to use this

- Boss says "port my app to iOS" → wrap existing.
- Boss says "I want it on the App Store" → wrap.
- Boss just wants a web app → don't bother; use the base scaffold alone.

## Gotchas

- Native builds need Xcode (iOS) and Android Studio (Android). Document
  these as one-time setup if the boss hasn't run them before.
- `cap sync` must run after every web rebuild before native testing.
