# Infinity — Changelog

Human-readable record of notable changes. Schema-bearing changes cite their
migration; the migration files in [`core/db/migrations/`](../core/db/migrations/)
are the source of truth for DB state (run `infinity migrate` to confirm what's
actually applied — see the "Migrations" hard rule in [`CLAUDE.md`](../CLAUDE.md)).

Newest first.

---

## 2026-06-02 — Durable plans ("the Cortex") + todo/plan unification

The agent gets a durable, steerable, verifiable plan, and the old ephemeral
"todo checklist" is folded into it so there's ONE concept shown the same way in
chat and on the dashboard. Full wiring in
[`ARCHITECTURE.md`](../ARCHITECTURE.md#durable-plans-the-cortex--todoplan-unification-2026-06-02).

### 1. Plan substrate — plan → execute → verify → replan
Migrations `116_mem_plans.sql` (tables + realtime + RLS) and
`117_seed_planning_skill.sql` (the `plan-and-verify` default skill), both applied
to prod. New `core/internal/plan/` package + `plan_create` / `plan_update` /
`plan_verify` / `plan_get` / `plan_list` tools (pinned). A plan survives
compaction, restart, and session boundaries — `PlanProvider` re-injects the
active plan + next step every turn, so a long task resumes where it left off.
**Verify-before-done is enforced in code:** a `verify_required` step can't be
marked done until `plan_verify` records passing evidence; a fail blocks the step
and prompts a replan. The cognition (how to decompose / when to verify) lives in
the skill + soul rule #11, never in Go.

### 2. todo/plan unification — one substrate, two synced views
`todo_write` is now an alias that writes the plan substrate
(`plan.Store.SyncChecklist`) instead of a separate `mem_runs.meta.todos` list. A
background build binds its plan to the **parent** chat session (`run_binding.go`
now carries `parentSession`), so the boss sees it in his conversation and on the
dashboard, not on the throwaway child session. The background `mem_runs` row is
now execution telemetry only (running?, current file), not a second checklist.
- **Dashboard:** a plan is a `kind:"plan"` card on the **Agent Work board** with a
  `4/7` step-progress bar; tapping it opens the full step timeline in ObjectViewer
  (shared `PlanTimeline`). There is **no separate `/plans` page** — a plan is
  agent work, so it lives on the board.
- **Chat:** the pinned dock renders the same plan via `usePlan` →
  `GET /api/plans/active` + realtime, so chat and dashboard stay in sync.

### 3. Context intelligence + ops
- `agent.GatedProvider` + `SubstantiveQuery`: a deterministic relevance gate that
  skips the heavy behavioral providers (proven lessons, reflection chains) on bare
  greetings, so the memory chain scales without per-turn token bloat.
- `INFINITY_AUTO_COMPRESS=true` in prod — observations promote to episodic memory
  daily (runs on the active model, not Anthropic-only).
- `ConnectorsSection.tsx` migrated to `<ResponsiveModal>` (reuse-first rule).
- Dashboard reorganized: Agent Work → Upcoming · Follow-ups → Surfaced · Pursuits
  · Todos.

---

## 2026-05-30 — "Steal from the field" pass (nanobot / Hermes / openclaw recon)

Three improvements adopted after reviewing the latest from nanobot, Hermes
(`NousResearch/hermes-agent`), and openclaw. Full recon notes live in the
session that produced them.

### 1. GEPA promotion gates — self-improvement can't make a skill dumber
`core/internal/voyager/optimizer.go`, `voyager.go`, `core/cmd/infinity/serve.go`

The GEPA optimizer rewrites a skill's `SKILL.md` to improve it, then persists
viable candidates as proposals. Two new hard gates run before a candidate is
allowed onto the Pareto frontier, guarding the classic GEPA drift failure:

- **Contract-preservation** (`preservesContract`, pure/structural): rejects a
  candidate that changes the skill `name`, drops a declared
  `required_environment_variables` / `requires_toolsets` / `fallback_for_toolsets`
  key, deletes a `## ` section heading, or shrinks below 50% of the original.
- **Semantic-drift** (`Manager.filterByDrift`, embedding-based): rejects a
  candidate whose embedding cosine to the original is `< 0.82`
  (env `INFINITY_GEPA_MIN_SIMILARITY`). Degrades to a logged no-op when only the
  dev stub embedder is wired, so optimization still runs without a model.

An embedder is now threaded through `voyager.Config` → `Manager`. Joins the
existing structural gates (≤15KB, valid frontmatter, non-empty, non-identical).

> Not included: a *benchmark*-regression gate ("higher quality but −5% on a
> suite = reject") needs an eval corpus we don't have yet — that's the
> SessionDB-mined-evals build, tracked separately.

### 2. Surface return-path — the dashboard acts, not just shows
Migration `084_surface_item_actions.sql` (applied to prod) + surface contract +
new `POST /api/surface/action` + `SurfaceCard`. Full doc:
[`docs/surface-return-path/README.md`](surface-return-path/README.md).

Surfaced items can now carry boss-tappable **actions**. Tapping one books a
`mem_runs` row (`kind=surface.action`) and seeds an autonomous agent turn
prompted with the action's intent + item context, then surfaces a live,
navigation-proof spinner. Generic + schema-driven; the `surface_item` tool now
nudges the agent to attach actions to anything actionable.

### 3. Persistent named peers — keep a specialist on retainer
`core/internal/agent/delegate.go`

`delegate` gained `agent_name` + `persist`. With both set, the sub-agent runs
in a stable `peer:<name>` session that is **not** torn down on return, so a
later consult with the same name resumes its accumulated context (a long-lived
`researcher` / `coder`). Ephemeral default unchanged; live peers are LRU-capped
at 6.

---

## Recently shipped (schema-bearing, by migration)

These landed over the preceding weeks; recorded here so the picture is complete.
Each cites its migration; read the file for specifics.

- **`083_realtime_rls_read_policies`** — RLS read policies for realtime-replicated
  `mem_*` tables.
- **`082_extensions_cli`** — `extensions` substrate (`kind=cli`) for agent
  self-provisioning of CLI tools, with human-in-the-loop auth (pause → auth URL
  → resume → verify).
- **`080`/`081`** — legacy skill-version archival; curiosity legacy index scoped
  to untagged rows only.
- **`077`–`079`** — durable follow-up / inbox-triage email bodies (full HTML
  cached at surface time so the dashboard reads an email even if the connector
  is later revoked) + inbox-triage cron and pointer.
- **`075`/`076`** — inbox-triage + duplicate-skill consolidation.
- **`067`/`068`** — agent teams substrate + the `coordinate_agent_team` default
  skill.
- **`069`–`074`** — default skill seeds: coding discipline, cloud workspace,
  GitHub, SaaS productivity, research/content, "work on a repo".
- **`065`/`060`/`061`** — stealth/agentic browser + document skills.
- **`058`** — native Google Calendar sync (rich attendees/organizer/conference
  shapes), backing the dashboard Upcoming card + RSVP.
- **`057`** — embeddings on training examples (Gym / plasticity).
- **`055`** — trust batch id (batch-approve trust contracts).

> Default skills are seeded via migrations, not the embed/scaffold path — see
> the "Ship default skills via migration" convention.

## Recently shipped (by subsystem, from git history — weeks of 2026-05-16 → 05-30)

Larger work that landed over the preceding two weeks and wasn't yet recorded in
a doc. Grouped by area; cite the package/component for specifics. These are
*not* exhaustively re-documented in `ARCHITECTURE.md` yet — a per-subsystem
architecture pass is a worthwhile follow-up. Recorded here so nothing is
silently undocumented.

### Canvas + Cloud workspace + Cloud browser
The biggest area of the period. Studio's Canvas became a real coding surface
and Jarvis got a cloud computer of his own.
- **Cloud Workspace integration for Canvas** — the agent's `/workspace` on the
  Railway workspace service is browsable + editable from Studio; canvas fs/git
  routes via `bridge.Router` per session (Mac bridge vs Cloud bridge).
- **Cloud browser + session management** — live agentic browser sessions with
  Studio "Stop" control; **Camofox stealth-browser bridge** behind the existing
  `browser_*` contract, selectable per session via bridge preferences. Setup:
  [`docs/camofox/SETUP.md`](camofox/SETUP.md).
- **Canvas preview** — workspace-bridge supervisor + Core `/api/canvas/preview`
  proxy (JWT-exempt) so a running dev server previews from any device; refined
  iframe-load logic to avoid unnecessary reloads.
- **Document generation + workspace proxy** — generate docs into the workspace
  and download/preview them via `/api/workspace/download`.
- **Coding-experience polish** — live tool-input streaming, authoritative
  line-tracking for edits, real diffs for Jarvis-edited files in Monaco,
  surfacing Jarvis's coding activity in chat. Setup for the Mac bridge:
  [`docs/canvas/SETUP.md`](canvas/SETUP.md).

### Connectors (Composio) — SaaS reach
- **Composio webhook ingestion** with HMAC **signature verification**; failures
  surface as findings/surface items.
- **Multi-account handling** — fresh-link flow, friendly account labels (alias >
  oauth email) across Follow-ups + Calendar, per-account cache status.
  (`core/internal/connectors/`, `/api/connectors/composio/*`.)

### Native Google Calendar + RSVP
- `058_calendar_native.sql` — native sync of rich attendee/organizer/conference
  shapes into `mem_calendar_events`; Accept/Decline RSVP from the dashboard
  Upcoming card; per-account labels. (`core/internal/calendar/`.)

### Agent teams
- `067_agent_teams.sql` + `068_seed_coordinate_agent_team_skill.sql` — a
  multi-agent team substrate + chat settings to configure team aggressiveness,
  parallelism, and per-team budgets. **Delegate spawn limiting** (cap fan-out;
  ephemeral non-UUID child ids) landed alongside. (Composes with the new
  persistent-named-peers work above.)

### CLI extensions — self-provisioning
- `082_extensions_cli.sql` — `mem_extensions` (`kind=cli`) so the agent installs
  + authenticates its own CLI tools, with a human-in-the-loop auth flow
  (pause → auth URL → resume → verify). Tracked via `runs.KindExtension`.

### Server-tracked progress (`mem_runs`)
- `035_mem_runs.sql` + `runs.Track` / `useRuns` / `<RunIndicator>` — every long
  server action books a `mem_runs` row so the spinner survives navigation,
  refresh, focus loss, and a second device. Now the canonical pattern (the
  surface return-path above is its newest consumer). Hard rule in
  [`CLAUDE.md`](../CLAUDE.md) → "Server-tracked progress".

### Studio + proactive polish
- **`/lab`** — `/code-proposals` + `/gym` folded into one page; a healing
  checklist routes cron failures + repeated tool errors there.
- **Heartbeat findings lifecycle** — open / resolved / dismissed, so the
  activity feed only shows live findings.
- **Push notifications** with user preferences; background-build notifications.
- **Pull-to-refresh + realtime** across pages; URL-backed tab state; removed
  redundant manual refresh buttons in favor of realtime.
- **Cost recording + intervention scoring**; curiosity scan tuned to suppress
  low-confidence / low-value questions.

### Reliability + correctness
- **Central active-model resolution** in the agent loop — cron, workflow,
  delegate, and WS all honor the boss's selected model through one resolver
  (replaced a partial cron-model fix that defaulted to Codex).
- **Logging severity** split — successes → stdout, failures → stderr, so Railway
  stops tagging info lines as errors (hard rule in `CLAUDE.md`).
- **Em-dash sanitizer** at the LLM boundary + a one-time codebase strip.
- **Stranded-turn / stranded-run recovery on boot** — any `mem_turns` /
  `mem_runs` row still in-flight at startup is swept closed so the UI never
  spins forever after a redeploy/crash. (`memory.TurnStore.RecoverStranded`,
  `runs.Tracker.RecoverStranded`.)
- **Local time provider** — injects the boss's CST `<current_time>` every turn.

### Voice (Phase 8) hardening
- GPT Realtime over WebRTC: structured transcript events, realtime session
  persistence + reconnection, voice identity-instruction preservation, realtime
  instruction-overflow guardrails, and a voice error-reporting endpoint.
  (`core/internal/voice/`, Studio voice components.)

---

*Maintenance note: when you ship a change worth remembering, add an entry here.
Schema changes cite their migration; behavioral changes cite the package/file.*
