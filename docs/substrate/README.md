# The Assembly Substrate

This is the trail through the substrate that makes Infinity an agent that
**assembles** workflows rather than one that runs hardwired Go verticals.
It is the build-out of [Rule #1](../../CLAUDE.md#rule-1--the-agent-assembles-you-do-not-hardwire-it).

> **The principle.** Infinity has APIs, MCP servers, native tools, queues,
> persistent memory, the internet, and a surface to write and run code.
> The goal is an agent that takes a workflow described in *natural
> language* and *assembles it* from those building blocks. Go is for the
> substrate — the tool, the queue, the contract, the loop. Never for the
> cognition. A prompt in a `.go` file means the wrong thing was built.

Each phase below adds **building blocks** (generic, schema-driven
contracts) the agent composes against — never a feature.

| Phase | Building block | Status |
|---|---|---|
| **1a** | Generic dashboard **surface contract** | ✅ shipped |
| **1b** | **Skill-authoring loop** — the agent makes a skill live | ✅ shipped |
| **2** | Durable **workflows + job queue** | ✅ shipped |
| **3** | Runtime **self-extension** (MCP / REST-API tool wiring) | ✅ shipped |
| **4** | **Verification** — eval scorecards + workflow validation | ✅ shipped |
| **5** | **World model** — entity graph + agent-owned goals | ✅ shipped |
| **6** | **Initiative + economics** — outbound reach, budgets, scheduling | ✅ shipped |

---

## Phase 1a — the generic surface contract

**The problem it replaces.** The old `followup_scoring.go` was a hardwired
vertical: a bespoke `mem_followups.importance` column, a Go scorer with the
ranking rubric frozen in a `const` string, wired into the heartbeat, drawn
by a Gmail-shaped `FollowUpsCard`. Every new source of "important things"
would have needed its own column, prompt, migration, and widget.

**The building block.** One generic table — `mem_surface_items` (migration
`016_surface_items.sql`) — that *any* producer writes ranked, structured
items into. Studio renders them generically. A new surface appears on the
dashboard with zero new table, zero new loader, zero new widget.

### The contract

```
mem_surface_items
  surface           which dashboard region — free-form: "followups",
                    "alerts", "digest", "insights", … (the agent invents these)
  kind              semantic type for the icon — "email","alert","metric",…
  source            who produced it — a skill name, connector slug, cron, "agent"
  external_id       stable id from the source system → upsert-on-rerun, no dupes
  title/subtitle/body/url   display payload
  importance        0-100 ranking, NULL = unranked
  importance_reason one line explaining the score
  metadata (jsonb)  arbitrary structured payload — the part downstream skills read
  status            open | snoozed | done | dismissed
  expires_at        optional TTL for ephemera (a digest entry stale tomorrow)
```

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/016_surface_items.sql`](../../core/db/migrations/016_surface_items.sql) | The table + dedup/read indexes + realtime publication |
| Store | [`core/internal/surface/`](../../core/internal/surface/) | `types.go` (Item, Status, Patch) + `store.go` — the **only** thing that touches the table |
| Tools | [`core/internal/tools/surface_tools.go`](../../core/internal/tools/surface_tools.go) | `surface_item` (upsert) + `surface_update` (status/rank/snooze) — the agent-facing contract |
| API | [`core/internal/dashboard/api.go`](../../core/internal/dashboard/api.go) | `loadSurface` — groups open items by `surface`; added to the `/api/dashboard` payload as `surfaceItems` |
| Studio type | [`studio/lib/dashboard/types.ts`](../../studio/lib/dashboard/types.ts) | `SurfaceItem` + the `surface` arm of the `DashboardItem` union |
| Studio card | [`studio/components/dashboard/SurfaceCard.tsx`](../../studio/components/dashboard/SurfaceCard.tsx) | The ONE generic renderer — `kind`→icon, importance→chip, grouped by `surface` |
| Studio viewer | [`studio/components/dashboard/ObjectViewer.tsx`](../../studio/components/dashboard/ObjectViewer.tsx) | `SurfaceBody` — renders body + metadata generically |

### The data flow

```
producer (skill recipe / connector / cron / agent mid-conversation)
   │  calls
   ▼
surface_item tool ──► surface.Store.Upsert ──► mem_surface_items
                                                   │ realtime
                                                   ▼
GET /api/dashboard → loadSurface (group by `surface`)
                                                   │
                                                   ▼
DashboardClient → one <SurfaceCard> per surface group → tap → ObjectViewer
```

The agent never writes the table with raw SQL. `surface_item` **is** the
contract — the boundary the LLM assembles against.

---

## Phase 1b — the skill-authoring loop

**The problem it replaces.** The agent could *propose* a skill
(`skill_propose` → a `candidate` row in `mem_skill_proposals`) but could
not make one **live**. The registry loaded skills from the filesystem
only; a runtime-authored skill was not invocable until a redeploy. The
self-authoring loop was open.

**The building block.** A `skill_create` tool plus a runtime activation
path on the registry. A skill the agent writes is invocable *this session*
and durable across restarts.

### How it closes

| Piece | Path | What |
|---|---|---|
| `skill_create` tool | [`core/internal/skills/registry_tools.go`](../../core/internal/skills/registry_tools.go) | The agent authors a skill: name, description, **body (the recipe — the judgment lives here)**, trigger phrases, risk level |
| `Registry.Put` | [`core/internal/skills/registry.go`](../../core/internal/skills/registry.go) | Adds the skill to the in-memory index **and** persists it via the Store — live now, durable later |
| `Store.InsertProposal` | [`core/internal/skills/store.go`](../../core/internal/skills/store.go) | The candidate path — drops a row in `mem_skill_proposals` for boss approval |

### The risk split

- **`risk_level: low`** (a pure recipe — no executable file) → `Registry.Put`
  → **live immediately**, invocable via `skills_invoke` this session.
  Persisted to `mem_skills` / `mem_skill_versions` / `mem_skill_active`; the
  boot-time `MaterializeActiveSkills` + `Reload` chain re-hydrates it on the
  next restart, so it is durable.
- **`risk_level: medium` and up** (or anything carrying executable code) →
  `Store.InsertProposal` → a `candidate` in the Skills tab for the boss to
  promote. Risk earns review; recipes do not.

### The payoff loop

```
boss (natural language): "triage my inbox every 30 min and surface what matters"
   │
   ▼
agent writes a SKILL.md body (the rubric — the "hard rules" — lives HERE)
   │  skill_create  (risk=low → live)
   ▼
agent wires a cron_create_agent to invoke it on a schedule
   │
   ▼
each run: skills_invoke → recipe pulls email via Composio → ranks it →
          surface_item for the important ones
   │
   ▼
dashboard renders the "followups" surface generically — zero Go for the feature
```

The Go written across Phase 1 is **substrate only**: a table, a store, two
tools, a registry method, a generic card. The *cognition* — the triage
rubric — lives in a skill body the agent wrote, versioned and improvable.

---

## Phase 2 — durable workflows + the job queue

**The problem it replaces.** A "workflow" was implicit — a cron prompt, or
whatever the agent did in a single turn. A complex multi-step process
collapsed into either one fragile mega-turn (lost on a restart) or
hardwired Go. There was no first-class, durable, *resumable* workflow.

**The building block.** A workflow is now a first-class object. The agent
assembles one from natural language (an ordered step list); a Go **engine**
runs it as a state machine that persists after every step — so a process
restart resumes mid-workflow exactly where it left off. Skills are single
recipes; workflows chain them — plus tools, sub-agent turns, and human
checkpoints — into processes that run over hours or days.

### The contract

A workflow is a list of **steps**, each one of four kinds:

| Kind | Spec | What it does |
|---|---|---|
| `tool` | `{"tool": "surface_item", "args": {…}}` | Invoke a native / MCP tool |
| `skill` | `{"skill": "inbox-triage", "args": {…}}` | Invoke a skill |
| `agent` | `{"prompt": "…"}` | Run a sub-agent turn, capture its answer |
| `checkpoint` | `{"message": "Review before I send"}` | Pause for boss approval |

String values in a step's spec can reference earlier state —
`{{steps.N.output}}` (a prior step's output) or `{{input.KEY}}` (a
run-level input). The engine resolves these before each step runs. That
templating is what lets the agent *chain* steps without the engine knowing
anything domain-specific.

### Schema

```
mem_workflows       the reusable definition — name + ordered step list
mem_workflow_runs   one execution instance — status, accumulated context,
                    current_step. THE durable thing.
mem_workflow_steps  per-run step state — status, attempt/max_attempts,
                    output, error. Materialized at run start so the run is
                    self-contained (editing the definition never disturbs
                    an in-flight run).
```

Run lifecycle: `pending → running → (paused at checkpoint) → done | failed
| cancelled`. Step lifecycle: `pending → running → done | failed |
skipped | awaiting`.

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/017_workflows.sql`](../../core/db/migrations/017_workflows.sql) | The three tables + the engine's hot-query index + realtime publication |
| Store | [`core/internal/workflow/store.go`](../../core/internal/workflow/store.go) | The persistence boundary — `StartRun` materializes step rows; `ClaimRunnable` locks the oldest runnable run; step/run transitions; `ReclaimOrphans` |
| Engine | [`core/internal/workflow/engine.go`](../../core/internal/workflow/engine.go) | The background worker — one step per tick, retry-with-attempts, checkpoint→pause, `{{…}}` resolution |
| Types | [`core/internal/workflow/types.go`](../../core/internal/workflow/types.go) | `Workflow`, `Run`, `Step`, `StepDef`, the `Executor` interface |
| Executor | [`core/cmd/infinity/workflow_executor.go`](../../core/cmd/infinity/workflow_executor.go) | The concrete `Executor` — dispatches `tool`/`skill`/`agent` steps; `checkpointSurfacer` puts paused runs on the dashboard |
| Tools | [`core/internal/tools/workflow_tools.go`](../../core/internal/tools/workflow_tools.go) | `workflow_create` / `_run` / `_status` / `_resume` / `_cancel` / `_list` — the agent-facing contract |
| Studio | [`core/internal/dashboard/api.go`](../../core/internal/dashboard/api.go) `loadWork` + [`ObjectViewer.tsx`](../../studio/components/dashboard/ObjectViewer.tsx) `WorkBody` | Workflow runs flow through the **Agent Work board** (`pending→queued`, `running→running`, `paused→awaiting`, terminal→`done`). Each run carries its step list inline — tapping the Kanban card opens the ObjectViewer drawer with the full step state-machine, and "Discuss with Jarvis" edits it. **No separate workflows page** — the Kanban *is* the workflow view. |

### The data flow

```
agent: workflow_create / workflow_run ──► workflow.Store ──► mem_workflow_runs (pending)
                                                                  │
   ┌──────────────────────────────────────────────────────────────┘
   ▼
workflow.Engine (background worker, 1 step / tick)
   │  ClaimRunnable → next pending step → resolve {{…}} → Executor.Execute
   │     • tool   → tools.Registry
   │     • skill  → skills.Runner
   │     • agent  → agent.Loop (fresh isolated session)
   │     • checkpoint → pause run, surface a card via the surface contract
   ▼
persist step output → AdvanceRun → next tick … → run done
```

The engine is dependency-light: it knows `tool / skill / agent /
checkpoint` and a state machine. *What* the steps are is the agent's
judgment, stored as data. The Go is pure substrate.

### Durability

- Every step transition is a DB write — the run's state is never only in
  memory.
- On boot, `ReclaimOrphans` resets any step left `running` by a process
  that died back to `pending`; the engine re-runs it.
- Steps retry up to `max_attempts` (default 3) before the run fails.
- A `checkpoint` step pauses the whole run and surfaces a card on the
  `approvals` dashboard region; `workflow_resume` clears it.

---

## Phase 3 — runtime self-extension

**The problem it replaces.** The agent's toolset was fixed at build time.
The MCP registry (`mcp.yaml`) is embedded in the binary — wiring a new
server meant a rebuild + redeploy. The agent could never say *"I need a
capability I don't have"* and get it.

**The building block.** A `mem_extensions` table + an `extensions.Manager`
that activates runtime-registered capability providers into the live tool
registry. The agent registers an extension, it's usable *this session*,
and it re-activates from the DB on the next boot — same durability model
as the skill-authoring loop. Two kinds today:

| Kind | What it is | Becomes |
|---|---|---|
| `http_tool` | Any REST endpoint, described once | A native tool `ext_<name>` — `{{param}}` placeholders in url/headers/body filled from call args |
| `mcp` | A remote MCP server (url + transport + auth) | All its tools, registered under `<name>__<tool>` |

**Secrets never touch the DB.** An `mcp` extension's auth references an env
var *name* (`auth_token_env`), never a value — the same rule the embedded
`mcp.yaml` follows.

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/018_extensions.sql`](../../core/db/migrations/018_extensions.sql) | `mem_extensions` + realtime publication |
| Types | [`core/internal/extensions/types.go`](../../core/internal/extensions/types.go) | `Extension`, `Kind`, `MCPConfig`, `HTTPToolConfig` + config parsing |
| Store | [`core/internal/extensions/store.go`](../../core/internal/extensions/store.go) | CRUD on `mem_extensions` |
| HTTP tool | [`core/internal/extensions/http_tool.go`](../../core/internal/extensions/http_tool.go) | `HTTPTool` — generic REST tool, satisfies `tools.Tool` structurally |
| Manager | [`core/internal/extensions/manager.go`](../../core/internal/extensions/manager.go) | `LoadAll` (boot), `Register` (runtime), `activate`, `Remove` |
| Tools | [`core/internal/extensions/tools.go`](../../core/internal/extensions/tools.go) | `extension_register` / `extension_list` / `extension_remove` |
| MCP hook | [`core/internal/tools/mcp.go`](../../core/internal/tools/mcp.go) `ConnectServer` | Connects one MCP server into a live process |

### The data flow

```
agent: extension_register ──► extensions.Manager.Register
                                  │  activate()
                                  ├── http_tool → NewHTTPTool → tools.Registry.Register
                                  └── mcp       → tools.MCPManager.ConnectServer → tools.Registry
                                  │
                                  ▼
                              mem_extensions (durable)
                                  │  on next boot
                                  ▼
                              Manager.LoadAll → re-activates everything enabled
```

The agent now genuinely compounds its own toolset. Need a weather API?
`extension_register` kind=http_tool, and `ext_weather` is live. Need
Linear? Register its MCP server and the Linear tools appear — no redeploy.

---

## Phase 4 — the verification substrate

**The problem it replaces.** As the agent assembled more — skills,
workflows, runtime tools — there was no way to know whether an assembly
*actually works*, or to notice when one quietly regressed. Trust gated
individual tool calls; nothing tracked outcomes over time.

**The building block.** A generic outcome ledger (`mem_evals`) + a
scorecard rollup. Every skill run, workflow run, or tool use can record an
outcome; a scorecard turns a subject's history into a success rate plus a
recent-vs-historical trend, so a degrading capability is *visible*. The
workflow engine auto-records every run — verification scales with the
autonomy it's checking.

Plus `workflow_validate` — the cheap "verify the assembly before you
commit it" check: kinds valid, each step's spec well-formed, every
`{{steps.N.output}}` reference points backward. It catches the structural
mistakes without running anything.

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/019_evals.sql`](../../core/db/migrations/019_evals.sql) | `mem_evals` outcome ledger + realtime publication |
| Package | [`core/internal/eval/eval.go`](../../core/internal/eval/eval.go) | `Store.Record` + `Store.Scorecard` (success rate, recent-vs-prior trend, regression flag) |
| Tools | [`core/internal/eval/tools.go`](../../core/internal/eval/tools.go) | `eval_record` / `eval_scorecard` |
| Auto-record | [`core/internal/workflow/engine.go`](../../core/internal/workflow/engine.go) `finishRun` + `EvalRecorder` | Every workflow run records `success`/`failure` on completion |
| Validation | [`core/internal/workflow/validate.go`](../../core/internal/workflow/validate.go) `ValidateSteps` | Static structural check of a step list |
| Validate tool | [`core/internal/tools/workflow_tools.go`](../../core/internal/tools/workflow_tools.go) `workflow_validate` | The agent-facing "check before you run" tool |

### How it composes

A scorecard works across *any* assembled thing — `subject_kind` is
`skill | workflow | tool | extension`. The same rollup that tells the boss
"the `inbox-triage` skill is at 92% and steady" tells the agent "the
`morning-brief` workflow dropped to 40% — regressing — investigate before
relying on it." Verification is one substrate, not per-kind plumbing.

> Honest scope: full *sandboxed dry-run* execution (side-effect-free
> rehearsal of a workflow) is not in this phase — `workflow_validate` is
> the static-check version, and the `checkpoint` step kind (Phase 2)
> covers human-in-the-loop verification. Sandboxed rehearsal is future
> work.

---

## Phase 5 — the world model + agent-owned goals

**The problem it replaces.** Honcho models the *boss*; `mem_memories` is
episodic recall. Neither is a structured, queryable model of the boss's
*world* — the people, projects, accounts, and threads the agent acts on,
and how they relate. And `mem_pursuits` is the boss's dashboard goals —
the agent had no durable goals of its *own*, no way to say "I'm working
toward X, here's my plan, I'm blocked on Y" and have that persist.

**The building block.** Two things, one package:

1. **The world model** — `mem_entities` (nodes: person / project / account
   / thread / …) + `mem_entity_links` (typed edges). The agent builds and
   queries a structured graph of the boss's world. `attributes` is
   free-form and *merges* on update, so the model accretes.
2. **Agent-owned goals** — `mem_agent_goals`, each carrying a *living
   plan* the agent re-writes as it makes (or fails to make) progress, a
   running progress narrative, and a `last_progress_at` the
   autonomous-pursuit loop watches.

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/020_world_model.sql`](../../core/db/migrations/020_world_model.sql) | `mem_entities`, `mem_entity_links`, `mem_agent_goals` + realtime |
| Types | [`core/internal/worldmodel/types.go`](../../core/internal/worldmodel/types.go) | `Entity`, `LinkView`, `Goal`, `PlanItem`, `GoalPatch` |
| Store | [`core/internal/worldmodel/store.go`](../../core/internal/worldmodel/store.go) | Entity upsert/get/search/link (attributes merge) + goal upsert/list/update |
| Tools | [`core/internal/worldmodel/tools.go`](../../core/internal/worldmodel/tools.go) | `entity_upsert` / `_link` / `_get` / `_search` · `goal_set` / `_update` / `_list` |
| Autonomous pursuit | [`core/internal/proactive/agent_goals.go`](../../core/internal/proactive/agent_goals.go) `AgentGoalChecklist` | Heartbeat checklist — resurfaces goals that are blocked, due soon, or stalled |

### The autonomous-pursuit loop

`goal_set` creates a durable objective. The agent records progress with
`goal_update` — which *appends* to the goal's narrative and bumps
`last_progress_at`. On every heartbeat tick, `AgentGoalChecklist` scans for
goals that are **blocked**, **due within 48h**, or **stalled** (no progress
in 3 days) and emits a Finding — so a goal the agent set and forgot gets
pulled back into view instead of rotting. The agent re-plans or closes it.

That closes the loop: the agent doesn't just *do tasks*, it *holds
objectives* and is reminded of them until they're resolved.

---

## Phase 6 — initiative + economics

**The problem it replaces.** An always-on agent that can only be *reacted
to* is half an agent. It had no policy for reaching the boss (everything
was either a silent dashboard write or nothing), no awareness of what it
costs to run, and no way to order one workflow after another.

**The building blocks.** Three, completing the agency loop:

1. **Initiative** — a `notify` tool with an urgency-routing policy:
   `urgent` → Web Push to the phone now, `normal` → a dashboard card,
   `low` → batched into the next digest (`notification_digest` flushes the
   batch as one push). Every notification is logged to `mem_notifications`.
2. **Economics** — a cost ledger (`mem_cost_events`) + `cost_record` /
   `budget_status`. The agent records what it spends and reads a rollup
   against `INFINITY_BUDGET_USD`, so it can throttle expensive work instead
   of burning the budget blind.
3. **Dependency-aware scheduling** — `mem_workflow_runs.depends_on`. A run
   can wait on another run finishing; the engine's `ClaimRunnable` skips a
   run whose dependency isn't `done` yet.

### Code map

| Layer | Path | What |
|---|---|---|
| Schema | [`core/db/migrations/021_initiative.sql`](../../core/db/migrations/021_initiative.sql) | `mem_notifications`, `mem_cost_events`, `mem_workflow_runs.depends_on` |
| Package | [`core/internal/initiative/initiative.go`](../../core/internal/initiative/initiative.go) | `Store` (notification + cost ledgers, budget rollup) + `Notifier` (the urgency policy) |
| Tools | [`core/internal/initiative/tools.go`](../../core/internal/initiative/tools.go) | `notify` / `notification_digest` / `cost_record` / `budget_status` |
| Deliverer | [`core/cmd/infinity/initiative_deliverer.go`](../../core/cmd/infinity/initiative_deliverer.go) | Concrete `Deliverer` — Web Push for urgent, the Phase 1 surface contract for normal |
| Scheduling | [`core/internal/workflow/store.go`](../../core/internal/workflow/store.go) `ClaimRunnable` / `StartRun` | `depends_on` — run-after-run ordering |

### How it composes

The initiative layer doesn't invent a new delivery channel — `urgent`
goes through the existing `push.Sender`, `normal` goes through the Phase 1
`surface_item` contract. The agency loop is now closed: the agent **holds
goals** (Phase 5), **assembles workflows** to pursue them (Phase 2),
**verifies** they work (Phase 4), **extends itself** when it lacks a
capability (Phase 3), **surfaces** results (Phase 1), and **reaches the
boss** with the right urgency at a cost it can account for (Phase 6).

> Honest scope: `INFINITY_BUDGET_USD` is read from the environment; the
> agent records cost events explicitly via `cost_record` (automatic
> per-LLM-call cost capture is future work). Dependency-aware scheduling
> is single-dependency (`depends_on` is one run id, not a DAG).

---

## Operational notes

- **Migrations `016`–`021` back this substrate** and are applied to prod
  (`016_surface_items`, `017_workflows`, `018_extensions`, `019_evals`,
  `020_world_model`, `021_initiative`). Per
  [`AGENTS.md`](../../AGENTS.md#migrations--verify-never-assert), verify
  with `cd core && railway run --service core -- go run ./cmd/infinity migrate`
  — every line should read `skip`.
- Every substrate package's tools register only when a `DATABASE_URL` is
  configured — the `Register*Tools` funcs no-op without a pool, so a
  chat-only deployment still boots cleanly.
- `skill_create` low-risk activation works with the registry alone; the
  candidate path needs the Store (DB).
- The workflow engine, the heartbeat goal checklist, and the surface
  expiry sweep all degrade quietly if their migration hasn't been applied
  — they log and skip rather than crashing the boot.
- **Env knobs:** `INFINITY_BUDGET_USD` sets the cost-rollup limit (unset =
  no limit). Web Push for urgent notifications needs the `VAPID_*` env
  vars — without them `notify urgent` still logs + falls back to a surface
  card.

## The whole loop, end to end

Once the substrate is in place, a single natural-language request flows
through every phase:

> *"Every weekday morning, pull my calendar, draft prep notes for each
> meeting, surface them, but check with me before booking anything — and
> keep an eye on whether this is actually useful."*

1. The agent **assembles a workflow** (Phase 2): connector poll → an
   `agent` step that drafts prep → `surface_item` steps → a `checkpoint`
   before any booking.
2. If it needs a capability it lacks — a specific calendar API — it
   **self-extends** (Phase 3) with `extension_register`.
3. It **validates** the workflow (Phase 4) with `workflow_validate`, then
   wires a `cron` to run it each morning.
4. Results land on the dashboard through the **surface contract**
   (Phase 1); the checkpoint pauses the run and **notifies** the boss
   (Phase 6) at `normal` urgency.
5. It records the recurring intent as an **agent goal** (Phase 5); the
   heartbeat resurfaces it if it stalls.
6. Every run **auto-records an eval** (Phase 4); a `eval_scorecard` tells
   the boss — and the agent — whether the whole thing is working.

No Go was written for that feature. Go was written once, for the
substrate. That is Rule #1.
