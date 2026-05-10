# Infinity

**A single-user AI agent with a real memory.**
Not a chatbot. Not a wrapper. A permanent cognitive substrate that remembers everything, learns continuously, and gets sharper the longer you use it.

> Hermes lights up on demand. Nanobot keeps a tiny loop. OpenClaw runs skills.
> Infinity does all three — and remembers every observation, fact, and decision in a provenance-tracked graph that compounds for years.

---

## What Infinity is

Infinity is your **always-on personal AI**, designed for one user — you — across every device. It's a cognitive layer, not a tool. Every conversation, every tool call, every observation becomes a memory. Every memory has a source. Every fact can be traced back to where it came from. Nothing is forgotten unless you tell it to forget.

Behind the scenes Infinity runs as **two services on Railway** plus **Postgres + pgvector**:

- **Core** — a small, fast Go binary that hosts the agent loop, MCP client, tool registry, memory subsystem, skills runtime, proactive heartbeat, and cron + sentinel scheduler.
- **Studio** — a mobile-first Next.js 14 app with eight tabs covering live chat, sessions, memory, skills, heartbeat, trust queue, cron + sentinels, and an audit log.
- **Database** — Postgres 16 with pgvector for embeddings, full-text search, and a graph-style memory store. Hosted on Supabase (free tier works fine).

---

## Why Infinity wins

### 1. Memory is the substrate, not a feature
Twelve `mem_*` tables form a real brain: observations, summaries, semantic memories, sources, relations, profiles, graph nodes, graph edges, audit, lessons. Every event in the agent loop fires a hook. Every hook funnels into capture. Every capture writes provenance. When Infinity cites a fact, it can show you the chain that produced it.

### 2. Triple-stream retrieval with Reciprocal Rank Fusion
Memory recall isn't just vector search. It's three parallel streams:
- **BM25** keyword search via Postgres `tsvector` + `websearch_to_tsquery`
- **Vector** similarity via pgvector HNSW (384-dim)
- **Graph** traversal — entities extracted by Haiku, BFS 2-hop across the memory graph

Then fused with RRF (k=60) and diversified across sessions. This is how Infinity surfaces the *right* memory, not just a similar one.

### 3. Continuous learning, not one-shot context
Observations get compressed into episodic memories by Claude Haiku with strict-JSON entity extraction. Episodic clusters consolidate into semantic memories on a nightly cron. A privacy filter (`memory.StripSecrets`) runs at the capture boundary so secrets never hit the database.

### 4. Skills are filesystem-native and OpenClaw-compatible
Drop a `SKILL.md` with YAML frontmatter into `./skills/` and Infinity loads it. Symlink from `~/.openclaw/workspace/skills/<name>` and existing skills work unmodified. Skills run in a sandbox tier matching their risk level — process jail today, container + Trust Contract for high-risk on the roadmap.

### 5. Proactive, not reactive
A heartbeat ticker runs every 30 minutes. It checks overdue outcomes, open patterns, failing skills, and queues anything that needs your approval into a Trust Contract queue. An IntentFlow detector classifies every user turn — silent / fast / full — so Infinity knows when to think hard and when to stay out of the way.

### 6. Cron + Sentinels make the agent autonomous
Schedule recurring agent runs (`mem_crons`). Define watchers that fire on webhooks, file changes, or memory events (`mem_sentinels`). Each fire dispatches through a skill chain with cooldown protection. The agent doesn't just respond — it acts.

### 7. Mobile-first, iOS-Safari-hardened
Built for the phone. `100dvh`, safe-area insets, 44×44 touch targets, 16px form fields, sticky composer (never fixed), WebSocket auto-reconnect on `pageshow` + `focus` + `visibilitychange` so it survives Safari background-tab kills.

---

## Architecture map

```
                 ┌──────────────────────┐
   browser ────► │  Studio (Next.js 14) │   8 tabs · mobile-first · WSS
                 └──────────┬───────────┘
                            │ HTTPS + WSS
                            ▼
                 ┌──────────────────────────────────────────────┐
                 │  Core (Go 1.26)                               │
                 │                                               │
                 │  ┌────────────────┐    ┌──────────────────┐  │
                 │  │ Agent Loop     │◄──►│ Tool Registry     │  │
                 │  │ (intentionally │    │ native + MCP      │  │
                 │  │  small)        │    │ websearch · fetch │  │
                 │  └───┬────────────┘    │ codeexec · memory │  │
                 │      │                 └──────────────────┘  │
                 │      │ HookEmitter                            │
                 │      ▼                                         │
                 │  ┌────────────────┐    ┌──────────────────┐  │
                 │  │ Hooks Pipeline │───►│ Memory Subsystem │  │
                 │  │ 12 events      │    │ store · search   │  │
                 │  │ async capture  │    │ compress · forget│  │
                 │  └────────────────┘    │ provenance · RRF │  │
                 │                         └────────┬─────────┘  │
                 │  ┌────────────────┐              │            │
                 │  │ Skills Runtime │              │            │
                 │  │ sandbox tiers  │              │            │
                 │  │ trigger match  │              │            │
                 │  └────────────────┘              │            │
                 │  ┌────────────────┐              │            │
                 │  │ Proactive      │              │            │
                 │  │ Heartbeat      │              │            │
                 │  │ IntentFlow     │              │            │
                 │  │ Trust queue    │              │            │
                 │  └────────────────┘              │            │
                 │  ┌────────────────┐              │            │
                 │  │ Cron + Sentinels│             │            │
                 │  │ robfig/cron     │             │            │
                 │  └─────────────────┘             │            │
                 └──────────────────────────────────┼────────────┘
                                                    │ pgxpool + pgvector-go
                                                    ▼
                                  ┌──────────────────────────────┐
                                  │  Postgres + pgvector          │
                                  │                               │
                                  │  12 mem_* tables (Phase 3)    │
                                  │  mem_skills + runs (Phase 4)  │
                                  │  mem_heartbeats + trust (P5)  │
                                  │  mem_crons + sentinels (P6)   │
                                  │  Supabase session pooler      │
                                  └──────────────────────────────┘
```

### Data flow at a glance

**Write path** — every event → Hooks Pipeline → SHA-256 dedup → `StripSecrets` → `mem_observations` → 384-dim embedding → FTS doc → `mem_audit` → (opt) Haiku compression → `mem_memories` + `mem_memory_sources`.

**Read path** — query → BM25 + pgvector + graph traversal in parallel → RRF fusion → session diversification → injected as system-prompt prefix on the next turn.

**Acting path** — agent loop → tool call (native or MCP) → result back → hook fires → memory writes → next iteration.

---

## Why Infinity vs the alternatives

| Capability | **Infinity** | Hermes | Nanobot | OpenClaw |
|---|:---:|:---:|:---:|:---:|
| Always-on, single-user agent | ✅ | ✅ | ✅ | ✅ |
| Persistent memory across sessions | ✅ 12-table mem store | ⚠️ session only | ❌ | ⚠️ basic |
| Triple-stream recall (BM25 + vector + graph) | ✅ | ❌ | ❌ | ❌ |
| Reciprocal Rank Fusion + session diversification | ✅ | ❌ | ❌ | ❌ |
| Provenance — every memory cites its source | ✅ `mem_memory_sources` | ❌ | ❌ | ❌ |
| LLM-driven memory compression (Haiku) | ✅ | ❌ | ❌ | ❌ |
| Privacy filter at capture boundary | ✅ `StripSecrets` | ⚠️ | ❌ | ⚠️ |
| Skills as filesystem (`SKILL.md` + frontmatter) | ✅ OpenClaw-compatible | ❌ | ❌ | ✅ |
| Sandboxed skill execution by risk tier | ✅ process jail (container WIP) | ❌ | ❌ | ⚠️ |
| MCP client (stdio + SSE) | ✅ | ✅ | ✅ | ✅ |
| Proactive heartbeat (agent initiates) | ✅ 30m ticker + checklist | ❌ | ❌ | ❌ |
| Trust contract approval queue | ✅ | ❌ | ❌ | ❌ |
| Intent classification per turn (silent/fast/full) | ✅ Haiku detector | ❌ | ❌ | ❌ |
| Cron-scheduled agent runs | ✅ robfig/cron | ❌ | ❌ | ❌ |
| Sentinels (webhooks · file · memory · poll) | ✅ webhook live, others scaffolded | ❌ | ❌ | ❌ |
| Audit log for every memory operation | ✅ `mem_audit` | ❌ | ❌ | ❌ |
| Mobile-first UI, iOS Safari hardened | ✅ | ⚠️ | ❌ | ⚠️ |
| Self-evolving skill curriculum (Voyager) | ⚠️ substrate ready | ❌ | ❌ | ❌ |
| Deploys as two Railway services + Supabase | ✅ | varies | varies | varies |

**Bottom line.** Hermes is excellent at responding. Nanobot is a beautifully small loop. OpenClaw is a great skill runtime. Infinity is the only one designed from day one as a *permanent cognitive substrate* — memory-first, provenance-tracked, proactive, and self-improving — and it stays compatible with the OpenClaw skill format so nothing you've built is wasted.

---

## Coding via Claude Code (Max-subscription, ToS-clean)

Infinity codes by orchestrating the official `claude` CLI on a home Mac, not by leaking OAuth tokens off the machine. The agent's brain stays on Anthropic's API; only when the model decides to invoke a `claude_code__*` tool does the call hop through Cloudflare Tunnel to the Mac's `claude mcp serve`. Subscription billing applies for coding work; API billing for chat. No tokens move.

```
[iPhone Safari]      [Cloudflare Tunnel]      [Mac at home]
     │                       │                  caffeinated, plugged in
     ▼ WSS                   ▼ SSE+bearer            │
infinity-core ──────────────► coder.<dom>.dev ──────► mcp-proxy (8765)
  • LLM brain                  Access ZTNA               │ stdio
  • mcp.yaml: claude_code      • IDP for humans          ▼
  • ClaudeCodeGate             • bearer for Railway   claude mcp serve
  • Trust queue                                       Bash/Read/Write/Edit
                                                     /Grep/Glob/LS
```

Setup runbook: [`docs/claude-code/SETUP.md`](docs/claude-code/SETUP.md).
Trust queue gating defaults: `INFINITY_CLAUDE_CODE_BLOCK=bash,write,edit`.

## Honcho (dialectic peer modelling)

Optional sidecar from [plastic-labs/honcho](https://github.com/plastic-labs/honcho) that derives a continually-updated peer representation from interaction traces. Infinity treats it as a complement: the 12 `mem_*` tables remain the source of truth for facts and provenance; Honcho contributes the *who*-layer to the system prompt via `agent.CompositeMemory`. Set `HONCHO_BASE_URL` to enable. Setup: [`docs/honcho/SETUP.md`](docs/honcho/SETUP.md).

## GEPA (skill self-evolution, Hermes-class)

A Python sidecar ([`docker/gepa.Dockerfile`](docker/gepa.Dockerfile)) that runs a Genetic-Pareto loop over Anthropic Haiku to evolve `SKILL.md` files from execution traces. Phase 1 — instructions only, same scope Hermes Phase 1 ships. Hard-gated on size, frontmatter validity, and Trust-queue approval. Trigger via `POST /api/voyager/optimize`. Cost ~$0.05-0.20 per run. See [`docs/gepa/README.md`](docs/gepa/README.md).

---

## Phase status

| Phase | What | Status |
|---|---|---|
| 0 | Foundation: repo, CLI, health, studio shell, Docker, Railway | ✅ |
| 1 | Working text bot: agent loop, LLM provider, WebSocket, Live tab | ✅ |
| 2 | Tools + MCP: registry, websearch, filesystem, codeexec, httpfetch, Settings | ✅ |
| 3 | Memory: 12-table store, triple-stream retrieval, hooks, compression, Memory tab, provenance | ✅ |
| 4 | Skills: filesystem loader, sandbox tiers, agent tools, HTTP API, Studio tab | ✅ substrate · container sandbox WIP |
| 5 | Proactive: IntentFlow, WAL, Working Buffer, Heartbeat, Trust queue | ✅ substrate · WS auto-fire WIP |
| 6 | Cron + Sentinels + Voyager substrate | ✅ schemas + dispatch · curriculum + verifier WIP |
| 7 | Polish: audit log + viewer · cmd+K · sessions rewind · graph viewer | ⚠️ in progress |
| 8 | Voice (always-on phone-first interface) | — roadmap |

See [ARCHITECTURE.md](ARCHITECTURE.md) for the source-of-truth wiring diagram.

---

## Features

### Memory & Recall
- **12-table memory store** — observations, summaries, semantic memories, sources, relations, profiles, graph nodes, graph edges, node-observation links, audit, lessons, sessions
- **Triple-stream retrieval** — BM25 + pgvector HNSW + 2-hop graph BFS, fused with Reciprocal Rank Fusion (k=60)
- **Session diversification** — caps any single session at 3 hits per recall to prevent echo chambers
- **Provenance chain** — every memory links to its source observations via `mem_memory_sources`; `GET /api/memory/cite/:id` surfaces the full chain
- **Cascading staleness** — `MarkSuperseded` propagates through the memory graph
- **Haiku LLM compression** — strict-JSON entity extraction promotes raw observations into episodic memories
- **Nightly consolidation** — `infinity consolidate` runs decay, hot-reset, clustering, and auto-forget
- **Privacy-first capture** — `memory.StripSecrets` runs 10 regex patterns + `<private>` tag stripping at the boundary
- **SHA-256 dedup** — 5-minute window prevents observation spam
- **Audit trail** — every memory operation writes a `mem_audit` row (table#id target)
- **FTS with synonyms** — `infinity_search` config with graceful fallback on managed Postgres

### Agent Loop
- **Intentionally small loop** — nanobot-inspired, never imports memory/skills/hooks directly (interfaces only)
- **Streaming tokens** — delta / tool_call / tool_result / complete / error events over WebSocket
- **System-prompt prefix injection** — memory recall + skill suggestions fold in before the first LLM call
- **Multi-iteration tool loop** — up to N tool round-trips per user turn
- **12 lifecycle hooks** — UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure, TaskCompleted, and 7 more

### Tools & Integrations
- **Native tools** — `http_fetch` (allowlisted domains), `web_search` (Tavily), `code_exec` (sidecar), `remember`, `recall`, `forget`
- **MCP client** — stdio + SSE transports, namespaced as `<server>.<tool>`, hot-loaded from `config/mcp.yaml`
- **Pluggable LLM provider** — Anthropic, OpenAI, Google (stub) behind a single `Provider` interface
- **Pluggable embedder** — stub (deterministic) or HTTP sidecar (FastAPI), 384-dim vectors
- **Tool registry auto-exposes** — anything registered shows up in `/api/tools` and the agent's tool list

### Skills System (Phase 4)
- **Filesystem-native** — `SKILL.md` + YAML frontmatter, OpenClaw-compatible (drop-in symlink)
- **Risk-tiered sandboxing** — process jail (low/medium) → container (high/critical, WIP)
- **Trigger matching** — Token Jaccard + substring overlap, threshold 0.5
- **Agent-callable** — `skills.list`, `skills.invoke`, `skills.discover`, `skills.history`
- **Run history** — every invocation persists to `mem_skill_runs` with success rate
- **Versioning** — `mem_skill_versions` + `mem_skill_active` for rollback
- **Hot reload** — `POST /api/skills/reload` re-walks the filesystem

### Proactive Engine (Phase 5)
- **IntentFlow detector** — Haiku classifies every turn into silent / fast / full + Quiet Hours gate
- **WAL extractor** — regex captures corrections, preferences, decisions, dates, URLs into `mem_session_state`
- **Working Buffer** — at 60% context utilization, snapshots into `mem_working_buffer` for recovery
- **Heartbeat ticker** — every 30 minutes runs the default checklist (overdue outcomes, open patterns, failing skills)
- **Trust Contract queue** — anything risky lands in `mem_trust_contracts` for approval / denial / snooze
- **Outcome journal** — `mem_outcomes` tracks promised work and surfaces overdue items

### Cron & Sentinels (Phase 6)
- **Cron scheduler** — `mem_crons` rows + robfig/cron/v3 with UTC, standard 5-field parser
- **Two job kinds** — `system_event` (fixed session id) or `isolated_agent_turn` (fresh UUID per fire)
- **Failure tracking** — `failure_count` resets on success, transactional `last_run_*` updates
- **Schedule preview** — `POST /api/crons/preview` returns next-N fire times before saving
- **Sentinels** — `mem_sentinels` rows define watch_type + watch_config + action_chain + cooldown
- **Webhook trigger live** — `POST /api/sentinels/:id/trigger` enforces enabled + cooldown then dispatches
- **Skill dispatcher** — sentinels fire skill chains directly through the runner
- **Voyager substrate** — `mem_skill_proposals`, `mem_skill_failures`, `mem_skill_tests` ready for the curriculum + verifier

### Studio (Next.js 14)
- **Eight tabs** — Live · Sessions · Memory · Skills · Heartbeat · Trust · Cron · Audit (+ Settings)
- **Live tab** — streaming chat with tool-call cards, session header, sticky composer
- **Memory tab** — searchable list with tier badges, provenance chains, observation drill-down
- **Skills tab** — cards with last_run + success_rate, manual invoke, run history
- **Heartbeat tab** — interval config, recent runs, run-now button
- **Trust tab** — approval queue with approve / deny / snooze actions
- **Cron tab** — sub-tabs for crons + sentinels, schedule preview, enable toggles
- **Audit tab** — every memory operation, filterable by op
- **Settings tab** — provider config, model, env diagnostics
- **Hydration discipline** — deferred UUIDs, `suppressHydrationWarning` on locale-dependent renders
- **shadcn/ui primitives** — Button, Card, Badge, Tabs, Dialog, ContextMenu, etc.
- **Tabler Icons** — consistent icon set across the app

### Mobile (iOS Safari + Chrome)
- **`100dvh` not `100vh`** — survives the address bar dance
- **Safe-area insets** — `pt-safe` / `pb-safe` / `px-safe` utilities everywhere
- **44×44 touch targets** — `<Button>` defaults to `h-11`
- **16px form fields** — global `font-size: max(16px, 1rem)` defeats focus-zoom
- **`overscroll-behavior: contain`** — body + every scroll region
- **`scroll-touch` utility** — wraps `-webkit-overflow-scrolling: touch`
- **Sticky composer** — never fixed (avoids the iOS keyboard-jump bug)
- **WebSocket auto-reconnect** — on `pageshow` + `focus` + `visibilitychange`
- **`inputMode` everywhere** — `text` / `search` / `numeric` per field
- **No hover-only affordances** — long-press `ContextMenu` for secondary actions
- **Verified breakpoints** — 375px / 390px / 768px / 1280px

### Operations
- **Two-service Railway deploy** — `core/` and `studio/` with auto-detected Dockerfiles
- **Embedded migrations** — `//go:embed db/migrations/*.sql`, no `db/` in the runtime container
- **Graceful degradation** — missing DATABASE_URL → memory off, missing LLM → server still serves health + memory
- **Doctor command** — env check + DB ping + pgvector extension check + sidecar reachability
- **CORS open** — Studio and Core run on different Railway domains
- **Distroless Core image** — `golang:1.26-alpine` build → distroless runtime
- **Standalone Studio image** — `node:22-alpine` with `CI=true` to skip interactive prompts
- **Permissive but auditable** — every action shows up in `/api/memory/audit`

---

## Quickstart (local)

Prereqs: Go 1.22+, pnpm 11+, Postgres 16 with the `vector` extension, Docker (optional).

### 1. Database

```sh
docker run -d --name infinity-pg \
  -e POSTGRES_USER=infinity \
  -e POSTGRES_PASSWORD=infinity \
  -e POSTGRES_DB=infinity \
  -p 5432:5432 \
  pgvector/pgvector:pg16
```

### 2. Environment

```sh
cp .env.example .env
# fill in ANTHROPIC_API_KEY at minimum
```

### 3. Migrate + Run Core

```sh
cd core
DATABASE_URL=postgres://infinity:infinity@localhost:5432/infinity?sslmode=disable \
  go run ./cmd/infinity migrate
go run ./cmd/infinity serve --addr :8080
# verify: curl localhost:8080/health
```

`infinity migrate` reads migrations from the binary itself (no `--dir` flag needed in production). Pass `--dir core/db/migrations` to use the on-disk copy if you're iterating on schema.

### 4. Run Studio

```sh
cd studio
pnpm install
pnpm dev
# open http://localhost:3000
```

### 5. Diagnose

```sh
cd core
go run ./cmd/infinity doctor
```

### 6. Nightly consolidation (cron)

```sh
go run ./cmd/infinity consolidate           # decay, hot-reset, cluster, auto-forget
go run ./cmd/infinity consolidate --compress # also runs Haiku observation→memory promotion
go run ./cmd/infinity consolidate --dry-run  # preview deletions only
```

---

## Repository layout

```
infinity/
  core/                           # Go binary — self-contained, embedded migrations
    Dockerfile                    # build context = core/
    cmd/infinity/                 # cobra CLI: serve, migrate, doctor, consolidate
    config/mcp.yaml               # MCP server registry
    db/migrations/                # 001_init → 006_voyager (embedded into binary)
    internal/
      agent/                      # the intentionally-small loop
      llm/                        # Anthropic, OpenAI, Google + Haiku summarizer
      tools/                      # Registry, MCP client, native tools, memory tools
      memory/                     # store, search, RRF, compress, forget, provenance
      hooks/                      # 12-event pipeline, capture, privacy
      embed/                      # Embedder interface (stub | http sidecar)
      skills/                     # loader, sandbox, runner, registry tools, HTTP API
      intent/                     # IntentFlow detector + Quiet hours
      proactive/                  # WAL, working buffer, heartbeat, trust queue
      cron/                       # scheduler, executor, HTTP API
      sentinel/                   # manager, dispatcher, HTTP API
      server/                     # HTTP + WebSocket + JSON API
  studio/                         # Next.js 14 app router — self-contained
    Dockerfile                    # build context = studio/
    app/{live,sessions,memory,skills,heartbeat,trust,cron,audit,settings}/
    components/                   # TabFrame, ChatBubble, MemoryCard, SkillCard, …
    components/ui/                # shadcn primitives
    hooks/useChat.ts              # deferred-UUID session id
    lib/{api,ws,utils}.ts         # typed REST + WSClient with iOS reconnect
  docker/embed.Dockerfile         # optional FastAPI embed sidecar
  railway.toml                    # pins rootDirectory per service
```

---

## Deploying to Railway

Two services on Railway, plus Postgres on Supabase.

1. **Postgres on Supabase** (recommended). Settings → Database → Connection string → use the **session pooler** URL (`aws-1-us-west-1.pooler.supabase.com:5432`) with `sslmode=require`. Free tier is IPv4-friendly. Set as `DATABASE_URL` on Core.

2. **Core service**. Source from this repo. Settings → Source → **Root Directory**: `core`. Wire env: `DATABASE_URL`, `ANTHROPIC_API_KEY`, `LLM_PROVIDER`, `LLM_MODEL`, `HTTP_FETCH_ALLOWED_DOMAINS`, optional `TAVILY_API_KEY` / `MCP_CONFIG` / `INFINITY_AUTO_COMPRESS`.

3. **Studio service**. Same repo. Settings → Source → **Root Directory**: `studio`. Wire env: `NEXT_PUBLIC_CORE_URL=https://core.up.railway.app`, `NEXT_PUBLIC_CORE_WS_URL=wss://core.up.railway.app/ws`.

4. **Migrate once**: `cd core && DATABASE_URL=… go run ./cmd/infinity migrate`. Or set `infinity migrate && infinity serve` as the Core start command.

`railway.toml` pins `rootDirectory` per service so auto-detection lines up.

---

## Mobile invariants

iPhone Safari + Chrome are the primary targets. Verified at 375px / 390px / 768px / 1280px.

- `100dvh` everywhere, never `100vh` — `.h-app` / `.min-h-app` utilities
- `viewport-fit=cover` + `env(safe-area-inset-*)` — `pt-safe` / `pb-safe` / `px-safe`
- 16px minimum font on form fields (defeats iOS focus-zoom)
- 44×44 minimum touch targets — `<Button>` defaults to `h-11`
- `overscroll-behavior: contain` on body and scroll regions
- WebSocket auto-reconnect on `pageshow` + `focus` + `visibilitychange`
- Composer is `position: sticky`, never `position: fixed` (avoids the iOS keyboard-jump bug)
- Tabler Icons (`@tabler/icons-react`) only — no Heroicons, no Material

---

## Environment variables

| Var | Service | Purpose |
|---|---|---|
| `DATABASE_URL` | core | Supabase session pooler DSN |
| `ANTHROPIC_API_KEY` | core | primary provider, Haiku compressor, IntentFlow |
| `LLM_PROVIDER` | core | `anthropic` (default) \| `openai` \| `google` |
| `LLM_MODEL` | core | model id; default `claude-sonnet-4-5-20250929` |
| `LLM_SUMMARIZE_MODEL` | core | Haiku model for compression |
| `INFINITY_AUTO_COMPRESS` | core | `true` enables observation → memory promotion |
| `INFINITY_SKILLS_ROOT` | core | path to skills directory; default `./skills` |
| `INFINITY_HEARTBEAT_INTERVAL` | core | Go duration; default `30m` |
| `INFINITY_INTENT_MODEL` | core | Haiku model for IntentFlow |
| `HTTP_FETCH_ALLOWED_DOMAINS` | core | glob list for the http_fetch tool |
| `TAVILY_API_KEY` | core | enables web_search tool |
| `MCP_CONFIG` | core | path to `core/config/mcp.yaml` |
| `NEXT_PUBLIC_CORE_URL` | studio | https origin of core service |
| `NEXT_PUBLIC_CORE_WS_URL` | studio | wss origin + `/ws` path |

---

## License

Private. Lifts and ports from `rohitg00/agentmemory` (Apache-2.0) per the build plan.
