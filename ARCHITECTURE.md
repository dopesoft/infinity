# Infinity — architecture

This document is the source of truth for how the running system is wired up. It reflects the codebase after the Coding-bridge / Honcho / GEPA work landed on top of Phase 4-7 substrate. Where the spec describes something we have not built yet, that's called out explicitly in the **Gaps** section at the end.

## 1. Five Railway services + one database + one Mac

```
                                ┌────────────────────────────┐
        iPhone Safari ────► WSS │  Studio (Next.js 14)        │  infinity.dopesoft.io
                                │  studio-production-2ca0     │  (CNAME → studio.up.railway.app)
                                └─────────────┬──────────────┘
                                              │ HTTPS + WSS
                                              ▼
                  ┌─────────────────────────────────────────────────────┐
                  │  Core (Go 1.26 · cobra CLI · net/http)              │
                  │  agent loop · ClaudeCodeGate · CompositeMemory      │
                  │  honcho client · voyager.Optimizer                   │
                  └──────────┬───────────────────────────────┬──────────┘
                             │ pgxpool/v5 + pgvector-go       │ internal Railway HTTP
                             ▼                                ▼
              ┌──────────────────────────┐         ┌──────────────────────┐
              │  Postgres + pgvector     │         │  honcho   (8000)     │
              │  Supabase session pooler │◄────────┤  honcho-deriver       │
              │  aws-1-us-west-1 :5432   │         │  gepa     (8080)     │
              │  IPv4, sslmode=require   │         │  redis    (6379)     │
              └──────────────────────────┘         └──────────────────────┘

                             ▲ MCP/SSE w/ CF-Access service token
                             │
                  ┌──────────┴────────────────────────────────────┐
                  │ Cloudflare Tunnel  jarvis-mac  +  Access ZTNA │
                  └──────────┬────────────────────────────────────┘
                             │
                  ┌──────────▼────────────────────────────────────┐
                  │  Home Mac  (caffeinated, launchd-managed)     │
                  │  cloudflared (system daemon)                  │
                  │  mcp-proxy 127.0.0.1:8765 (user LaunchAgent)  │
                  │  claude mcp serve  ← Max subscription, OAuth  │
                  └───────────────────────────────────────────────┘
```

Each service has its own Dockerfile at the service root. `railway.toml` sets `rootDirectory` per service so Railway treats them as independent build contexts. All inter-service traffic flows over Railway's private network (`<service>.railway.internal:<PORT>`) — only core and studio have public ingress.

## 2. Boot sequence (`core/cmd/infinity/serve.go`)

```
1. llm.FromEnv()              → Provider (Anthropic | OpenAI | Google stub)
2. tools.NewRegistry()
   tools.RegisterDefaults()    → http_fetch, web_search, code_exec
   tools.LoadMCPConfig()       → reads MCP_CONFIG, falls back to embedded
                                  core/config/mcp.yaml (//go:embed)
   tools.MCPManager.Connect()  → stdio + SSE clients, namespaced "<server>__<tool>"
                                  • claude_code: SSE + Cloudflare Access service token
                                                 (CF-Access-Client-Id + CF-Access-Client-Secret)
                                                 → exposes Bash/Read/Write/Edit/etc.
3. pgxpool.New(DATABASE_URL)
   embed.FromEnv()             → stub | http (sidecar)
   memory.NewStore / NewSearcher
   memory.NewCompressor        (only when LLM_PROVIDER=anthropic)
   hooks.NewPipeline()
   hooks.RegisterDefaults      → wires capture into all 12 event hooks
   tools.RegisterMemoryTools   → remember, recall, forget
4. honcho.FromEnv()            → optional dialectic peer client (HONCHO_BASE_URL)
   if enabled, register two hooks (UserPromptSubmit, TaskCompleted) that
   mirror messages into Honcho via POST /v1/messages
5. proactive.NewTrustStore(pool) (built early so the Gate has it)
   agent.NewClaudeCodeGate(trust) → routes claude_code__{bash,write,edit}
                                    through mem_trust_contracts by default
                                    (overridable: INFINITY_CLAUDE_CODE_AUTOAPPROVE
                                                  INFINITY_CLAUDE_CODE_BLOCK)
6. skills.NewRegistry("./skills") → registry, store, runner, agent tools, HTTP API
7. agent.New({
       LLM, Tools, Skills,
       Memory = CompositeMemory(searcher, honcho.NewMemoryProvider(client)),
       Hooks  = PipelineAdapter{pipeline},
       Gate   = ClaudeCodeGate{trustStore},
   })
8. proactive.NewHeartbeat / intent.New / proactive.NewAPI
9. cron.New / cron.Scheduler.Start
   sentinel.NewManager / sentinel.Manager.Reload
   voyager.New (extractor + verifier + discovery)
   voyager.NewAPI                 → /api/voyager/{status,proposals,optimize}
10. server.New + server.Start
    signal.NotifyContext(SIGINT, SIGTERM)
```

Every component degrades gracefully if its dependency is missing: no `DATABASE_URL` → memory/proactive/cron skip; no LLM provider → loop disabled, server still serves health + memory APIs; no Anthropic provider → compressor disabled, capture pipeline still runs; no `HONCHO_BASE_URL` → Honcho hooks no-op; no `GEPA_URL` → `/api/voyager/optimize` returns 503.

## 3. Package layout

```
core/
  cmd/infinity/
    main.go            cobra root
    serve.go           boot wiring (the diagram above)
    migrate.go         applies //go:embed db/migrations/*.sql
    doctor.go          env + DB ping + pgvector extension check
    consolidate.go     nightly cron entrypoint (--compress flag)

  db/migrations/       embedded into the Go binary
    001_init.sql       vector + pg_trgm + uuid-ossp
    002_memory.sql     12 mem_* tables (Phase 3)
    003_search.sql     infinity_search FTS config
    004_skills.sql     mem_skills, mem_skill_versions/active/runs
    005_proactive.sql  mem_session_state, mem_working_buffer,
                       mem_intent_decisions, mem_heartbeats[+_findings],
                       mem_outcomes, mem_trust_contracts, mem_patterns
    006_voyager.sql    mem_crons, mem_sentinels,
                       mem_skill_proposals/failures/tests

  config/            mcp.yaml + embed.go (//go:embed, package config) so the
                     distroless container ships the canonical MCP registry
  internal/
    agent/             loop.go (nanobot-inspired), gate.go (ToolGate interface
                       + AllowAll + IsClaudeCodeTool helper),
                       composite_memory.go (chains N MemoryProviders)
    llm/               Provider interface + Anthropic, OpenAI, Google stub,
                       AnthropicSummarizer (Haiku) for memory compression
    tools/             Tool interface, Registry, MCP client w/ headerRoundTripper
                       (auth=bearer | cloudflare_access), native tools,
                       memory tools, defaults
    memory/            store, search (BM25+vector+graph), rrf, compress,
                       privacy, forget, staleness, audit, provenance, list,
                       summarizer_adapter, types
    hooks/             pipeline, capture (privacy-first), 12 event constants,
                       defaults wiring, PipelineAdapter for agent.HookEmitter
    honcho/            client.go (HTTP), provider.go (MemoryProvider impl);
                       reads HONCHO_BASE_URL / HONCHO_API_KEY / HONCHO_WORKSPACE
                       / HONCHO_PEER; 60-sec cached representation
    embed/             Embedder interface, stub (deterministic), http (sidecar)
    skills/            types, loader (YAML+MD), registry, triggers (fuzzy),
                       sandbox_process[_unix|_other], runner, store,
                       registry_tools (4 agent tools), system_prompt,
                       agent_adapter, http
    intent/            flow.go (Haiku detector + Quiet hours), store
    proactive/         wal (regex extractor), buffer (60% threshold),
                       heartbeat (ticker), checklist (default checks),
                       trust (TrustStore), gate.go (ClaudeCodeGate routes
                       risky claude_code__* to mem_trust_contracts),
                       http (4 endpoints)
    voyager/           manager (state), extractor (SessionEnd → SKILL.md draft),
                       verifier (synthetic tests via Haiku), discovery
                       (tool-triplet patterns), optimizer.go (calls GEPA
                       sidecar, applies hard gates, persists proposals),
                       api.go (/status, /proposals, /optimize)
    cron/              types, scheduler (robfig/cron/v3),
                       executor_agent (cron→agent.Loop bridge), http
    sentinel/          types, manager, dispatcher (Log + Skill), http
    server/            server, health, ws, api, memory_api, audit_api

studio/
  app/
    layout.tsx         viewport-fit=cover, fonts, WebSocketProvider
    page.tsx           redirects to /live
    live/              Phase 1 chat
    sessions/          Phase 1
    memory/            Phase 3
    skills/            Phase 4
    heartbeat/         Phase 5
    trust/             Phase 5
    cron/              Phase 6 (with sub-tabs cron + sentinel)
    audit/             Phase 7
    settings/          Phase 2 MVP
  components/
    TabFrame.tsx       sticky header (logo + StatusPill + TabNav + ThemeToggle)
                       + main + FooterStatus
    TabNav.tsx         8 tabs: live / sessions / memory / skills / heartbeat /
                       trust / cron / audit
    Composer / ConversationStream / ChatBubble / SessionHeader / ToolCallCard
    MemoryCard / MemoryDetail / MetricCard / TierBadge / ProvenanceChain
    SkillCard / SkillDetail / RiskBadge
    StatusPill / FooterStatus / ThemeToggle
    ui/                shadcn primitives (button, card, badge, tabs, …)
  hooks/useChat.ts     deferred-UUID session id (avoids hydration mismatch)
  lib/api.ts           typed REST client (Phases 1-7)
  lib/ws/              WSClient + WebSocketProvider with iOS-Safari reconnect
                       on pageshow / focus / visibilitychange
```

## 4. HTTP API map

| Path | Method | Handler | Purpose |
|---|---|---|---|
| `/health` | GET | server.handleHealth | readiness + uptime |
| `/ws` | WS | server.handleWebSocket | streaming chat protocol (delta/tool_call/tool_result/complete/error) |
| `/api/status` | GET | server.handleStatus | version, provider, model, tools |
| `/api/sessions` | GET | server.handleSessions | in-memory loop sessions |
| `/api/tools` | GET | server.handleTools | registered tool descriptors |
| `/api/mcp` | GET | server.handleMCP | MCP server connection status |
| `/api/memory/counts` | GET | server.handleMemoryCounts | totals across mem_observations / mem_memories / graph |
| `/api/memory/search?q=` | GET | server.handleMemorySearch | triple-stream + RRF |
| `/api/memory/observations` | GET | server.handleObservations | recent raw observations |
| `/api/memory/memories?tier=&project=` | GET | server.handleMemoryList | filtered memory list |
| `/api/memory/cite/:id` | GET | server.handleMemoryCite | provenance chain |
| `/api/memory/audit?limit=&op=` | GET | server.handleAuditLog | mem_audit rows (table#id target) |
| `/api/skills` | GET | skills.API | list summaries (last_run + success_rate) |
| `/api/skills/reload` | POST | skills.API | re-walk filesystem |
| `/api/skills/:name` | GET | skills.API | full SKILL.md + frontmatter |
| `/api/skills/:name/runs?limit=` | GET | skills.API | recent mem_skill_runs |
| `/api/skills/:name/invoke` | POST | skills.API | manual run with JSON args |
| `/api/heartbeat` | GET | proactive.API | interval + recent runs |
| `/api/heartbeat/run` | POST | proactive.API | run-now button |
| `/api/trust-contracts?status=` | GET | proactive.API | approval queue |
| `/api/trust-contracts/:id/decide` | POST | proactive.API | approved\|denied\|snoozed |
| `/api/intent/recent?limit=` | GET | proactive.API | last N IntentFlow decisions |
| `/api/crons` | GET, POST | cron.API | list / upsert |
| `/api/crons/preview` | POST | cron.API | next-N fire times for a schedule |
| `/api/crons/:id` | DELETE | cron.API | delete |
| `/api/sentinels` | GET, POST | sentinel.API | list / upsert |
| `/api/sentinels/:id` | DELETE | sentinel.API | delete |
| `/api/sentinels/:id/trigger` | POST | sentinel.API | webhook entrypoint |
| `/api/voyager/status` | GET | voyager.API | manager state + counters |
| `/api/voyager/proposals?status=` | GET | voyager.API | list mem_skill_proposals |
| `/api/voyager/proposals/:id/decide` | POST | voyager.API | promote / reject |
| `/api/voyager/optimize` | POST | voyager.API | trigger GEPA on `{ "skill": "<name>" }` |

CORS is permissive (`*`) since Studio and Core run on different Railway domains. WebSocket origin is unrestricted for the same reason.

## 5. The agent loop (`core/internal/agent/loop.go`)

```
Run(ctx, sessionID, userMsg, out chan<- RunEvent):
  session.Append(user msg)
  hooks.Emit("UserPromptSubmit")

  systemPrompt := defaultSystemPrompt
  if memory:  systemPrompt = memory.BuildSystemPrefix(...) + systemPrompt
                              ← CompositeMemory chains Searcher (RRF, primary) + Honcho dialectic
  if skills:  systemPrompt = skills.MatchAndPrefix(userMsg, 5) + systemPrompt ← Phase 4

  for iter := 0; iter < maxToolIterations; iter++:
      provider.Stream(ctx, system, messages, tool defs, llm event chan)
      forward StreamText → EventDelta on out channel
      if no tool_calls: emit EventComplete + hooks.Emit("TaskCompleted"); return

      for each tool_call:
          emit EventToolCall + hooks.Emit("PreToolUse")
          decision := gate.Authorize(tool_call)              ← ToolGate
          if !decision.Allow:                                ← ClaudeCodeGate routes risky
              output := formatGatedOutput(...contract_id)    ← claude_code__{bash,write,edit}
              hooks.Emit("ToolGated")                        ← to mem_trust_contracts
          else:
              output := tools.Execute(tool_call)
          emit EventToolResult + hooks.Emit("PostToolUse" | "PostToolUseFailure")
          session.Append(role=tool, content=output, tool_call_id=...)
```

The loop is *intentionally small* — nanobot-inspired. New capabilities are added by:
- registering a `Tool` (native or MCP) — appears in `tools.Definitions()` automatically
- implementing `MemoryProvider` (used: `memory.Searcher` + `honcho.MemoryProvider`,
  chained via `agent.CompositeMemory`)
- implementing `SkillMatcher` (already done by `skills.Registry`)
- implementing `ToolGate` (currently `proactive.ClaudeCodeGate`; default `AllowAll`)
- emitting a hook (handled by `hooks.PipelineAdapter` wrapping `hooks.Pipeline`)

The loop never imports `memory`, `skills`, `hooks`, `honcho`, or `proactive` directly — they're attached via interfaces in `agent.Config`.

## 6. Memory subsystem

### Write path (Phase 3)
```
hooks.Pipeline.Emit(event)
  → pipeline goroutine
    → CaptureHook fires for matching events:
      - SHA-256 dedup, 5-min window
      - memory.StripSecrets   (10 regex patterns + <private> tag)
      - store.InsertObservation
      - embed.Embedder.Embed (384-dim)
      - update fts_doc
      - mem_audit row
      - if INFINITY_AUTO_COMPRESS=true and provider=anthropic:
           Compressor.LLMCompress (Haiku) → strict-JSON entity extraction
           promote observation → episodic memory
           write mem_memory_sources rows for provenance
```

### Read path (Phase 3)
```
agent.Run()
  → memory.Searcher.BuildSystemPrefix(query)
    → memory.Search(query, opts)
      → errgroup parallel:
         (1) BM25 via tsvector + websearch_to_tsquery (50 hits)
         (2) Vector via pgvector HNSW <=> distance (50 hits)
         (3) Graph: extract entities (Haiku) → BFS 2-hop in mem_graph_nodes/edges
      → rrf.Fuse(streams, k=60)
      → DiversifyBySession (skip when session has 3 hits)
    → format top N with attribution into a system-prompt block
```

### 12 mem_* tables (Phase 3)
- `mem_sessions`, `mem_observations`, `mem_summaries`, `mem_memories`
- `mem_memory_sources` (provenance linkage)
- `mem_relations`, `mem_profiles`
- `mem_graph_nodes`, `mem_graph_edges`, `mem_graph_node_observations`
- `mem_audit`, `mem_lessons`

## 7. Skills subsystem (Phase 4)

### Filesystem layout
```
$INFINITY_SKILLS_ROOT/                 # default ./skills
  weekly-standup-summary/
    SKILL.md                           # YAML frontmatter + Markdown body
    implementation.py                  # optional executable
  code-review/
    SKILL.md
```

Path convention is OpenClaw / Hermes-compatible: symlink `~/.openclaw/workspace/skills/<name>` into `./skills/<name>` and the loader picks it up unmodified.

### Frontmatter
```yaml
---
name: weekly-standup-summary
version: 1.2.0
description: Generate weekly standup from observations
trigger_phrases: ["weekly standup", "what did i do this week"]
inputs:
  - { name: week_offset, type: int, default: 0 }
outputs:
  - { name: summary, type: string }
risk_level: low                        # low | medium | high | critical
network_egress: none                   # "none" or [domain, ...]
last_evolved: 2026-04-22
confidence: 0.92
---
# Skill body (Markdown — instructions for the LLM if no implementation file)
```

### Sandbox tiers (`risk_level` → tier)
| Risk | Tier | Status |
|---|---|---|
| low | process jail | ✅ implemented (`sandbox_process_unix.go`: setpgid + restricted env + timeout context) |
| medium | process jail + network gate | ⚠️ network gate is structurally in place via `SandboxOpts.NetworkAllow` but not enforced at the HTTP transport layer yet |
| high | container | ❌ runner returns error; `docker/docker/client` integration is the next step |
| critical | container + Trust Contract | ❌ same, plus the queue path through `TrustStore` |

### Trigger matching
Token Jaccard + substring overlap, threshold 0.5. Ranked matches fold into the system prompt via `skills.SuggestionPrefix` ahead of the agent's first LLM call. The agent decides whether to invoke.

### Agent-callable tools
- `skills.list` — enumerate
- `skills.invoke` — execute by name + args (LLM-only skills return their formatted prompt; with-impl skills run in the sandbox)
- `skills.discover` — semantic-ish phrase search
- `skills.history` — recent runs

## 8. Proactive engine (Phase 5)

```
                ┌──────────────────────────────┐
  user msg ─────►│ IntentFlow Detector          │
                 │ Haiku → JSON {token, conf}  │
                 │ silent | fast | full         │
                 │ + Quiet Hours gate           │
                 └───────────┬──────────────────┘
                             │ store in mem_intent_decisions
                             ▼ (depth → DepthFor token)
                ┌──────────────────────────────┐
                │ WAL (regex extract)         │
                │ corrections / preferences /  │
                │ decisions / dates / URLs     │
                │ → mem_session_state          │
                └──────────────────────────────┘
                             │
                             ▼
                ┌──────────────────────────────┐
                │ Working Buffer (60% ctx)     │
                │ → mem_working_buffer         │
                └──────────────────────────────┘

every 30 min:
                ┌──────────────────────────────┐
                │ Heartbeat ticker             │
                │ DefaultChecklist:            │
                │  - overdue mem_outcomes      │
                │  - open mem_patterns         │
                │  - failing-skill detection   │
                │ → mem_heartbeats[+_findings] │
                │ → queue mem_trust_contracts  │
                └──────────────────────────────┘
```

The IntentFlow detector and WAL are *available* but not yet wired into the WebSocket handler — they're API-callable for now and will be folded into the per-turn capture pipeline next session.

## 9. Cron + Sentinels (Phase 6)

### Cron
- `mem_crons` rows define schedule + target (prompt) + job_kind
- `cron.Scheduler` wraps `robfig/cron/v3` with UTC location and the standard 5-field parser
- On each fire: `cron.AgentExecutor.ExecuteJob` runs the target prompt against `agent.Loop`
  - `system_event` → fixed session id `<name>-system`
  - `isolated_agent_turn` → fresh UUID per fire (writes to memory then dies)
- `last_run_*` columns updated transactionally; `failure_count` resets on success

### Sentinels
- `mem_sentinels` rows define watch_type + watch_config + action_chain + cooldown
- `sentinel.Manager` keeps an in-memory cache that mirrors the table; reload after upsert/delete
- `Trigger(id, payload)` enforces enabled + cooldown, then dispatches via `SkillDispatcher`
- The webhook watch_type runs through `POST /api/sentinels/:id/trigger` — done
- The `file_change`, `memory_event`, `external_api_poll`, `threshold` watch runtimes are not yet implemented; the schema and dispatch path are ready for them

## 10. Coding bridge — Claude Code over MCP + Cloudflare Tunnel

This is how Infinity codes without violating Anthropic's Consumer Terms: Core
on Railway never holds an OAuth token. Instead it speaks MCP-over-SSE to a
home Mac, which runs `claude mcp serve` under the boss's Max subscription.

```
Railway core ── MCP SSE  + CF-Access service-token headers ──┐
                                                              ▼
                  Cloudflare Tunnel  (existing jarvis-mac tunnel)
                  Cloudflare Access  (Self-hosted app: coder.dopesoft.io)
                    ├─ Email allow rule  (kai@dopesoft.io for browser access)
                    └─ Service Auth rule (Service Token: infinity-railway-core)
                                                              │
                                                              ▼
                  Home Mac (always-on, plugged in, caffeinated)
                    /Library/LaunchDaemons/com.cloudflare.cloudflared.plist
                    ~/Library/LaunchAgents/dev.dopesoft.mcp-proxy.plist
                      mcp-proxy --port 8765 --host 127.0.0.1 --server sse \
                                -- claude mcp serve     ← OAuth from Keychain
                    ~/Library/LaunchAgents/dev.dopesoft.caffeinate.plist
                    ~/Library/LaunchAgents/dev.dopesoft.claude-mcp.plist   (log namespace)
```

### Wiring on the core side
- `core/config/mcp.yaml` registers the `claude_code` server with
  `transport: sse`, `auth: cloudflare_access`,
  `cf_client_id_env: CF_ACCESS_CLIENT_ID`,
  `cf_client_secret_env: CF_ACCESS_CLIENT_SECRET`.
- `tools/mcp.go` resolves the headers via `headerRoundTripper` and attaches
  it to `mcp.SSEClientTransport.HTTPClient`. Two auth modes supported:
  `bearer` (Authorization: Bearer …) and `cloudflare_access`
  (CF-Access-Client-Id + CF-Access-Client-Secret).
- All 25 Claude Code tools register under the `claude_code__*` namespace
  (double underscore — Anthropic's tool name regex disallows dots).

### Authorization layer — `agent.ToolGate`
- `proactive.ClaudeCodeGate` intercepts every `claude_code__*` call.
- Default policy: `bash`, `write`, `edit` queue into `mem_trust_contracts`;
  read-only verbs (`read`, `ls`, `grep`, `glob`) pass through.
- Overridable per-deploy:
  - `INFINITY_CLAUDE_CODE_BLOCK="bash,write,edit"` (default)
  - `INFINITY_CLAUDE_CODE_AUTOAPPROVE=""` (default empty)
- When gated, the agent loop synthesizes a tool result of the form
  `BLOCKED: tool <name> requires the boss's approval ... Trust contract: <uuid>`
  so the model tells the user where to approve.
- Studio's `ToolCallCard` detects that string, renders a `Lock` icon and an
  "Approve in Trust tab" link that deep-links to `/trust?focus=<uuid>`.

### Mac setup
`docs/claude-code/` ships the runbook:
- `SETUP.md` — end-to-end ToS-clean walkthrough
- `install.sh` — idempotent: installs mcp-proxy, ensures launchd plists, removes
  any legacy `dev.dopesoft.cloudflared` user agent (system daemon owns it)
- `launchd/*.plist` — caffeinate, claude-mcp, mcp-proxy (no cloudflared
  plist — the existing system daemon serves `secr3t` + `vnc` + `coder`)

We re-use the existing `jarvis-mac` tunnel (UUID `8e5bd68f-…`) — `coder.dopesoft.io`
is added as a third ingress alongside SSH + VNC.

## 11. Honcho — dialectic peer modelling

Honcho ([plastic-labs/honcho](https://github.com/plastic-labs/honcho)) sits
beside Infinity's `mem_*` tables, not on top of them. Infinity remains the
source of truth for facts and provenance; Honcho contributes a continually
updated *peer representation* — the LLM's reasoned model of who the boss is.

```
core hook UserPromptSubmit ─┐
core hook TaskCompleted ────┤── POST /v1/workspaces/infinity/messages ─► honcho (api)
                            │                                              │
                            │                                              ▼ async
                            │                            ┌──────────────────────────┐
                            │                            │ honcho-deriver (worker)  │
                            │                            │ python -m src.deriver    │
                            │                            │ pulls from Redis queue,  │
                            │                            │ updates peer reps        │
                            │                            └──────────────────────────┘
                            │
agent.BuildSystemPrefix ────► CompositeMemory:
                                 1. memory.Searcher  (RRF over mem_*)
                                 2. honcho.MemoryProvider (60-sec cached
                                    representation pulled via /representation)
```

### Wiring
- `honcho.FromEnv()` reads `HONCHO_BASE_URL`, `HONCHO_API_KEY`,
  `HONCHO_WORKSPACE` (default `infinity`), `HONCHO_PEER` (default `boss`).
- Two hooks (`honcho.user`, `honcho.assistant`) mirror redacted messages.
  `memory.StripSecrets` runs *before* the hook fires — same redacted text
  Infinity stores locally.
- `agent.CompositeMemory` concatenates non-empty prefixes from each child
  provider. A Honcho outage downgrades to "RRF only", never breaks the turn.

### Runtime
- `docker/honcho/Dockerfile` clones upstream main, runs the FastAPI server.
- `docker/honcho-deriver/Dockerfile` is the same image with
  `CMD = python -m src.deriver` for the background worker.
- Both services share env via Railway reference vars (`${{core.DATABASE_URL}}`,
  `${{core.ANTHROPIC_API_KEY}}`).
- DB URL is rewritten at container startup: Supabase emits `postgresql://…`,
  Honcho's async SQLAlchemy wants `postgresql+psycopg://…`. A small sh
  snippet in the CMD prefixes the scheme without surfacing the secret.

## 12. Voyager / GEPA — skill self-evolution

Two coordinated loops, off-by-default via `INFINITY_VOYAGER=true`.

### Voyager creation (`core/internal/voyager/`)
- **Discovery** — every PostToolUse appends to a per-session window of tool
  names. Repeated N-tuples across sessions become candidates in
  `mem_skill_proposals`.
- **Extraction** — on SessionEnd, score recent observations against a
  heuristic. Above threshold → Haiku drafts a SKILL.md candidate.
- **Verification** — Haiku generates synthetic test cases. Instruction-only
  skills auto-promote; impl-bearing skills wait for human decide.

### GEPA improvement (`core/internal/voyager/optimizer.go` + `docker/gepa/`)
Hermes-style Genetic-Pareto prompt evolution for *existing* skills.

```
POST /api/voyager/optimize { "skill": "<name>" }
   → voyager.RunOptimizer:
       1. pull recent mem_skill_runs for the skill (failures + successes)
       2. POST docker/gepa/ sidecar at /optimize with traces + current SKILL.md
          GEPA flow:
            • Haiku reads failure traces → root-cause summary (1 call)
            • Haiku mutates SKILL.md targeted at the root cause (6 candidates)
            • Haiku scores each candidate against eval cases (6 calls)
            • returns Pareto-sorted by score, size
       3. pickWinner applies hard gates:
            • non-empty after trim
            • ≤15KB (mirror Hermes Phase 1 ceiling)
            • starts with "---" frontmatter
            • not byte-identical to original
       4. insert winner into mem_skill_proposals as candidate
       5. boss approves via existing /api/voyager/proposals/:id/decide
```

The sidecar (`docker/gepa/`) holds no state — it just runs Haiku calls and
returns candidates. Hard gates ride on the Go side so a sidecar
compromise can't bypass them. Cost per run: ~$0.05–$0.20.

## 13. Studio conventions

### Mobile-first invariants
Already enforced in `studio/app/globals.css`:
- `100dvh` everywhere — `.h-app` / `.min-h-app` utilities
- `viewport-fit=cover` + `pt-safe` / `pb-safe` / `px-safe`
- Form fields min 16px to defeat iOS Safari focus-zoom
- `overscroll-behavior: contain` on body and every scroller
- WebSocket auto-reconnect on `pageshow` + `focus` + `visibilitychange` (iOS Safari kills sockets on background)
- All shadcn buttons default to `h-11` (≥44px touch target)

### Hydration discipline
- No `Math.random()` / `crypto.randomUUID()` / `Date.now()` in `useState` initializers — defer to `useEffect`
- Every locale-dependent `<time>` and `<Badge>` renders the date wrapped with `suppressHydrationWarning` because UTC server vs client locale produces divergent text

### Build
- Studio Dockerfile uses `node:22-alpine` (pnpm 11 imports `node:sqlite` which needs Node 22+)
- `CI=true` + `NEXT_TELEMETRY_DISABLED=1` to avoid the `confirmModulesPurge` interactive prompt
- `pnpm-workspace.yaml` must contain `allowBuilds: { unrs-resolver: false }`

## 14. Deployment

```
Railway project: Infinity
  ├─ core            rootDirectory=core/            golang:1.26-alpine → distroless
  ├─ studio          rootDirectory=studio/          node:22-alpine → standalone
  │                  custom domain: infinity.dopesoft.io (CNAME → studio.up.railway.app)
  ├─ gepa            rootDirectory=docker/gepa/     python:3.12 + FastAPI + httpx
  ├─ honcho          rootDirectory=docker/honcho/   python:3.13 + plastic-labs/honcho main
  ├─ honcho-deriver  rootDirectory=docker/honcho-deriver/  same image, CMD = python -m src.deriver
  └─ redis           image: redis:7-alpine          (Honcho cache; private network only)

Postgres: Supabase session pooler (IPv4)
  aws-1-us-west-1.pooler.supabase.com:5432
  sslmode=require
  Used by both core (mem_* tables) and honcho (its own tables, shared schema for now)

Inter-service traffic: <service>.railway.internal:<PORT> (private network, no public ingress).
Only core + studio expose public HTTPS.

Mac coder bridge (orthogonal to Railway):
  Cloudflare Tunnel "jarvis-mac"  routes coder.dopesoft.io → 127.0.0.1:8765 on Mac
  Cloudflare Access policy "Railway" allows service tokens infinity-railway-core
                                       and railway-jarvis (Hermes legacy)
  Mac launchd: caffeinate, mcp-proxy, claude-mcp (claude mcp serve)
```

Environment variables that matter:

| Var | Service | Purpose |
|---|---|---|
| `DATABASE_URL` | core, honcho* | Supabase session pooler DSN; Honcho's CMD rewrites the scheme to `postgresql+psycopg://` at startup |
| `ANTHROPIC_API_KEY` | core, gepa, honcho* | primary provider; also drives Haiku compressor, IntentFlow, GEPA; Honcho uses for its own derivations |
| `LLM_PROVIDER` | core | `anthropic` (default) \| `openai` \| `google` |
| `LLM_MODEL` | core | model id; default `claude-sonnet-4-5-20250929` |
| `LLM_SUMMARIZE_MODEL` | core | Haiku model for compression; default `claude-haiku-4-5-20251001` |
| `INFINITY_AUTO_COMPRESS` | core | `true` enables observation→memory promotion (costs Haiku tokens) |
| `INFINITY_SKILLS_ROOT` | core | path to skills directory; default `./skills` |
| `INFINITY_HEARTBEAT_INTERVAL` | core | Go duration; default `30m` |
| `INFINITY_INTENT_MODEL` | core | override Haiku model for IntentFlow |
| `INFINITY_VOYAGER` | core | `true` enables Voyager extractor + verifier hooks |
| `INFINITY_CLAUDE_CODE_BLOCK` | core | comma list of `claude_code__` verbs to gate; default `bash,write,edit` |
| `INFINITY_CLAUDE_CODE_AUTOAPPROVE` | core | comma list that always passes through the gate |
| `HTTP_FETCH_ALLOWED_DOMAINS` | core | glob list for the http_fetch tool |
| `TAVILY_API_KEY` | core | enables web_search tool |
| `MCP_CONFIG` | core | path override for MCP registry; defaults to embedded `core/config/mcp.yaml` |
| `CLAUDE_CODE_TUNNEL_URL` | core | `https://coder.dopesoft.io/sse` |
| `CF_ACCESS_CLIENT_ID` | core | Cloudflare Service Token id (suffix `.access`) for `claude_code` MCP |
| `CF_ACCESS_CLIENT_SECRET` | core | Cloudflare Service Token secret |
| `HONCHO_BASE_URL` | core | `http://honcho.railway.internal:8000` (Railway private network) |
| `HONCHO_WORKSPACE` | core | default `infinity` |
| `HONCHO_PEER` | core | default `boss` |
| `GEPA_URL` | core | `http://gepa.railway.internal:8080` |
| `DB_CONNECTION_URI` | honcho, honcho-deriver | typically `${{core.DATABASE_URL}}`; CMD rewrites scheme |
| `CACHE_URL` | honcho, honcho-deriver | `redis://default@redis.railway.internal:6379/0?suppress=true` |
| `AUTH_USE_AUTH` | honcho, honcho-deriver | `false` (Railway private network is the perimeter) |
| `NEXT_PUBLIC_CORE_URL` | studio | https origin of core service (baked at build time) |
| `NEXT_PUBLIC_CORE_WS_URL` | studio | wss origin + `/ws` path |

## 15. Phase status (honest)

| Phase | Spec scope | What's live | Gaps |
|---|---|---|---|
| 0 | Repo + CLI + health + studio shell | ✅ all | — |
| 1 | Working text bot | ✅ all | — |
| 2 | Tools + MCP + Settings MVP | ✅ all | Settings tab depth — Phase 7 |
| 3 | Memory subsystem | ✅ all | Recall@10 benchmark fixture pending |
| 4 | Skills system | ✅ schema, registry, process-jail, agent tools, HTTP API, Studio Skills tab | Container sandbox for high/critical risk · network egress enforcement at the HTTP transport · Tests sub-tab in Studio · "+ New skill" / Import buttons · Edit + Disable + dropdown export/fork/archive |
| 5 | Proactive engine | ✅ IntentFlow detector, WAL, Working Buffer, Heartbeat, Trust queue, all schemas, HTTP APIs, Heartbeat + Trust Studio tabs | **Hierarchical memory access** struct (DepthFor exists; not wired) · **Compaction Recovery** flow on session start · IntentFlow + WAL + Buffer not yet auto-fired from the WS handler · Reverse Prompting / Curiosity / Pattern detector · Heartbeat checklist items: security scan, memory %, surprise · Live tab 3-column layout · Studio Heartbeat sub-tabs (Proactive tracker, Pattern recognition, Outcome journal, Curiosity loop, Surprise queue) · Phase 5 Studio components: ControlTokenBadge, IntentStream, ContextBudget, SuggestionCard, TrustGate · "Always allow this pattern" rules · bulk approve in Trust |
| 6 | Voyager + Cron + Sentinels | ✅ Cron scheduler + Sentinel manager + Skill dispatcher + schemas + HTTP APIs + Studio Cron+Sentinels tab | **Curriculum** generator · **Skill generator** (LLM-driven) · **Verifier** synthetic tests · **AutoSkill** failure-reflection-patch loop · Skill discovery hooks (regex pattern detection in observations) · Sentinel runtimes for non-webhook watch types (file_change, memory_event, external_api_poll, threshold) · Skills tab Candidate column population · NaturalLanguageScheduleInput live parser · Verification log sub-tab |
| 7 | Polish | ✅ Audit log endpoint + viewer; ✅ Honcho dialectic peer modelling; ✅ Claude Code coding bridge (25 tools via MCP + CF Access); ✅ GEPA skill optimizer sidecar; ✅ custom domain `infinity.dopesoft.io` | Command palette (cmd+K, cmdk lib) · Sessions rewind · Skills Tests sub-tab · Settings 10-section depth · Memory tab knowledge graph viewer · Backup/export · `infinity restore` · Doctor full diagnostic suite · Light/dark + animation polish |
| 8 | Voice | — | Skipped per direction |

## 16. Next-session priorities

If you're picking this up fresh, the highest-leverage gaps to close are:

1. **Wire IntentFlow + WAL + WorkingBuffer into the WebSocket handler.** Each user turn should classify → record → optionally extract → optionally append to buffer. The substrate is ready; this is one file edit in `internal/server/ws.go`.
2. **Compaction Recovery.** On session start, if the user message contains a `<summary>` tag or matches "where were we", read `mem_working_buffer` + `mem_session_state` and surface a "recovered from buffer" message.
3. **Skill discovery hooks.** Run the regex set from the Voyager spec against every observation in `hooks.CaptureHook`; insert detected candidates into `mem_patterns` and `mem_skill_proposals`. Surface them in the Skills tab as "candidate".
4. **Container sandbox.** `core/internal/skills/sandbox_container.go` with `docker/docker/client`. Unblocks high/critical-risk skills.
5. **Curriculum + Verifier.** Daily heartbeat sub-task that calls `mem_observations` clusters → LLM → `mem_skill_proposals`; verifier runs synthetic tests against the proposed implementation; promotion gates through `mem_trust_contracts`.

Phases 4-7 substrate is feature-complete enough that each gap above is a focused, scoped follow-up — no architectural rework needed.
