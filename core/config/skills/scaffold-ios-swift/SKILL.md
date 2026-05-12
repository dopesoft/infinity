---
name: scaffold-ios-swift
version: "1.0.0"
description: Scaffold a SwiftUI iOS app with an Xcode project and attach it to the session.
trigger_phrases:
  - new ios app
  - swift app
  - swiftui app
  - xcode project
  - build an iphone app
inputs:
  - name: slug
    type: string
    required: true
outputs:
  - name: project_path
    type: string
risk_level: high
network_egress:
  - github.com
  - swiftpackageregistry.com
confidence: 0.8
---

# Scaffold a SwiftUI iOS app

iOS apps don't run in a browser preview — Canvas shows the project files
and Xcode project structure; the boss runs the app in the iOS simulator
on their Mac.

## Steps

1. Pick a slug (kebab-case, will become the Xcode project name).
2. From `$INFINITY_CANVAS_ROOT/<slug>/`, generate a Swift Package Manager-based iOS app:

   ```bash
   mkdir -p <slug>
   cd <slug>
   swift package init --type executable
   ```

   Then write a minimal SwiftUI structure:

   - `Package.swift` — iOS 17 platform, target with `@main App`.
   - `Sources/<Slug>/App.swift` — `@main struct App: SwiftUI.App`.
   - `Sources/<Slug>/ContentView.swift` — initial view.

3. There is **no dev server**. Tell the boss to open the project in Xcode
   (`open Package.swift`) and run on a simulator. Canvas preview will
   show an "iOS project — no preview" empty state.
4. Attach via `POST /api/sessions/:id/project` with `project_template: "ios-swift"`. Leave `dev_port` unset (0).

## Why this template

The boss can iterate on SwiftUI views in Canvas's editor, then run the
build in Xcode on their Mac. Canvas's file editor still works for `.swift`
files via Monaco.

## Gotchas

- The SPM-based template doesn't generate a `.xcodeproj` — Xcode opens
  `Package.swift` directly.
- For a "real" iOS app needing entitlements, storyboards, or asset
  catalogues, you'll want a proper `.xcodeproj` (use `xcodegen` or
  start in Xcode and import).
