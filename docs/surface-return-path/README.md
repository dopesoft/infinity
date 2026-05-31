# Surface return-path — the dashboard acts, it doesn't just show

**Status: shipped 2026-05-30.** Migration `084_surface_item_actions.sql` applied to prod.

## What it is

The generic surface contract (`mem_surface_items`, see [`docs/substrate/README.md`](../substrate/README.md))
let the agent *push* ranked, structured items onto the dashboard: an alert, an
article, a flagged email, a digest entry. It was one-directional — the agent
wrote, Studio rendered, and the only way for the boss to *act* on a surfaced
item was to open a chat and type a request.

The **return-path** closes that loop. A surfaced item can now carry
boss-tappable **actions**. Tapping one fires an autonomous agent turn, prompted
with the action's intent + the item's full context, so the agent actually does
the thing — and a live, server-tracked spinner shows the work happening.

```
agent → surface_item(actions:[…]) → SurfaceCard renders buttons
   ↑                                                    │
   └──── boss taps "Summarize" → POST /api/surface/action ┘
                 → mem_runs row (kind=surface.action)
                 → autonomous agent turn (intent + item context)
                 → agent acts, surface_update's the item
```

This is the [nanobot MCP-UI](https://glama.ai/blog/2025-09-23-nanobot-by-obotai-architecting-real-mcp-agents-with-mcp-ui)
"return path" idea, mapped onto Infinity's existing generic surface +
server-tracked-runs substrates. No per-action Go, no per-card widget — Rule #1.

## The shape of an action

Stored as a `jsonb` array on `mem_surface_items.actions`. Each action:

```json
{ "id": "draft_reply",
  "label": "Draft reply",
  "intent": "Draft a reply to this email and save it as a draft.",
  "style": "primary" }
```

- `id` — stable, unique within the item; what the client POSTs back.
- `label` — short button text the boss sees.
- `intent` — the natural-language instruction the agent runs. **Server-side
  only — never shipped to the browser** (the dashboard DTO projects out
  `id`/`label`/`style`; `intent` stays in Postgres and is resolved by the
  endpoint).
- `style` — optional UI hint: `primary` | `default` | `danger`.

## Wiring (files)

| Layer | File | Role |
|---|---|---|
| Schema | [`core/db/migrations/084_surface_item_actions.sql`](../../core/db/migrations/084_surface_item_actions.sql) | adds `actions jsonb NOT NULL DEFAULT '[]'` |
| Contract | [`core/internal/surface/types.go`](../../core/internal/surface/types.go) | `surface.Action{ID,Label,Intent,Style}` + `Item.Actions` |
| Store | [`core/internal/surface/store.go`](../../core/internal/surface/store.go) | persists + reads actions; re-run never wipes a non-empty set |
| Write tool | [`core/internal/tools/surface_tools.go`](../../core/internal/tools/surface_tools.go) | `surface_item` accepts `actions`; description nudges the agent to attach them |
| Endpoint | [`core/internal/server/surface_action_api.go`](../../core/internal/server/surface_action_api.go) | `POST /api/surface/action {id, action_id}` → `runs.Track` → autonomous turn |
| Read DTO | [`core/internal/dashboard/api.go`](../../core/internal/dashboard/api.go) | `SurfaceAction` (client-safe: id/label/style only) |
| UI | [`studio/components/dashboard/SurfaceCard.tsx`](../../studio/components/dashboard/SurfaceCard.tsx) | renders buttons + `<RunIndicator>` |
| Client | [`studio/lib/api.ts`](../../studio/lib/api.ts) | `postSurfaceAction(id, actionId)` |

## Invariants it preserves

- **Server-tracked progress (hard rule).** The action books a `mem_runs` row
  (`runs.KindSurfaceAction`) so the spinner survives navigation, refresh, tab
  switch, and a second device — read via `useRuns({kind, targetId})` +
  `<RunIndicator mode="inline">`. The button's local `firing` state is only a
  momentary double-tap guard; the durable state is the server's.
- **Autonomous-turn guardrails.** The seeded turn runs under
  `tools.WithAutonomous(ctx)`, so the existing autonomous-only protections
  apply — most importantly, it still **cannot auto-resolve a follow-up email**
  (the boss dispositions his own inbox; see
  [`project_followups_never_auto_resolved`](../../core/internal/tools/surface_tools.go)).
- **Generic.** Any producer (a skill recipe, a connector poll, a cron, the
  agent mid-conversation) attaches actions and they render + run with zero new
  frontend or per-action Go.

## AGI-out-of-the-box

Shipped in the same PR so the loop is live the first time the boss tries it:
the `surface_item` tool description now instructs the agent to attach 1–3
useful actions whenever it surfaces something actionable (an article →
`Summarize`; an alert → `Investigate`). The boss never has to ask "now make the
card do something."

## Known follow-ups (not in this PR)

- Follow-up **emails** render through the separate `FollowUp` DTO / ObjectViewer
  path, not the generic `SurfaceCard`, so a "Draft reply" button on a follow-up
  email card is a future wiring task. Generic surfaced items (alerts / insights
  / digest / …) get actions today.
