# Infinity — architecture

This document is the source of truth for how the running system is wired up. It reflects the codebase after the Coding-bridge / Honcho / GEPA work landed on top of Phase 4-7 substrate. Where the spec describes something we have not built yet, that's called out explicitly in the **Gaps** section at the end.

## 1. Six Railway services + one database + one Mac

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
              │  IPv4, sslmode=require   │         │  plasticity (8080)  │
              │                          │         │  redis    (6379)     │
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
   memory.NewProceduralStore → attached to Searcher so the system prefix
                              injects top-K procedural memories per turn
                              (CoALA's procedural tier, populated by
                              voyager.Manager.OnSkillPromoted callback)
   memory.NewCompressor        (only when LLM_PROVIDER=anthropic) →
                              now auto-links new memories to top-4
                              cosine-nearest neighbours via 'associative'
                              edges (A-MEM, arXiv 2502.12110)
   hooks.NewPipeline()
   hooks.RegisterDefaults      → wires capture into all 12 event hooks
   memory.NewPredictionStore   → recorded by hooks.PredictionRecorder
                                 (PreToolUse writes expected, PostToolUse
                                 resolves with Jaccard surprise score)
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
8. proactive.NewHeartbeat with ComposeChecklists(DefaultChecklist,
   CuriosityChecklist) — heartbeat now scans for low-confidence memories,
   unresolved contradictions, uncovered graph mentions, high-surprise
   predictions, writing idempotent rows to mem_curiosity_questions
   intent.New / proactive.NewAPI
9. cron.New / cron.Scheduler.Start
   sentinel.NewManager / sentinel.Manager.Reload
   voyager.New (extractor + verifier + discovery + source_extractor)
   voyager.Manager.OnSkillPromoted(memory.ProceduralStore.UpsertFromSkill)
     → every promoted skill writes a tier='procedural' row
   voyager.NewAutoTrigger(voyagerMgr, voyager.NewOptimizer()).Start
     → background ticker watches mem_skill_runs for failing skills,
       auto-fires GEPA when failure rate ≥ threshold + cooldown elapsed
       (close-the-loop step Voyager was missing)
   voyager.NewAPI                 → /api/voyager/{status,proposals,optimize,code-proposals}
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
    consolidate.go     nightly cron entrypoint (--compress flag);
                       calls the 8-op sleep-time ConsolidateNightly
    reflect.go         metacognition entrypoint — walks recent sessions,
                       runs MAR critic persona via Haiku, writes
                       mem_reflections + auto-promotes lessons

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
    007_auth.sql       JWKS owner-claim + signup gate
    008_realtime.sql   supabase_realtime publication wiring
    009_canvas_projects.sql  mem_sessions project columns
    010_code_proposals.sql   mem_code_proposals (Voyager source extractor)
    011_agi_loops.sql  mem_reflections, mem_predictions,
                       mem_curiosity_questions, Pareto frontier columns
                       on mem_skill_proposals (frontier_run_id, score,
                       pareto_rank, gepa_metadata), procedural-tier
                       index, mem_relations(relation_type) index
    012_openai_oauth.sql · 013_session_usage.sql · 014_dashboard.sql ·
    015_connector_polls.sql
    016_surface_items.sql    the assembly substrate — see §18:
    017_workflows.sql        mem_surface_items (016), mem_workflows +
    018_extensions.sql       _runs + _steps (017), mem_extensions (018),
    019_evals.sql            mem_evals (019), mem_entities + _links +
    020_world_model.sql      mem_agent_goals (020), mem_notifications +
    021_initiative.sql       mem_cost_events + mem_workflow_runs.depends_on (021)
    022_plasticity.sql       Gym / plasticity ledgers: mem_training_examples,
                       mem_distillation_datasets/runs, mem_model_adapters,
                       mem_adapter_evals, mem_policy_routes

  config/            mcp.yaml + embed.go (//go:embed, package config) so the
                     distroless container ships the canonical MCP registry
  internal/
    agent/             loop.go (nanobot-inspired), gate.go (ToolGate interface
                       + AllowAll + IsClaudeCodeTool helper),
                       composite_memory.go (chains N MemoryProviders)
    llm/               Provider interface + Anthropic, OpenAI, Google stub,
                       AnthropicSummarizer (Haiku) for memory compression,
                       AnthropicCritic for session reflection (MAR persona)
    tools/             Tool interface, Registry, MCP client w/ headerRoundTripper
                       (auth=bearer | cloudflare_access), native tools,
                       memory tools, defaults
    memory/            store, search (BM25+vector+graph), rrf,
                       compress (now with A-MEM auto-linking),
                       privacy, forget, staleness, audit, provenance, list,
                       consolidate (8-op sleep regime),
                       procedural (CoALA's procedural tier — promoted-skill
                                    materializer + TopK retrieval),
                       reflection (metacognition; MAR critic persona),
                       predictions (Pre/Post pairing with Jaccard surprise),
                       summarizer_adapter, critic_adapter, types
    hooks/             pipeline, capture (privacy-first), 12 event constants,
                       defaults wiring, predict (PredictionRecorder hooks),
                       PipelineAdapter for agent.HookEmitter
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
                       heartbeat (ticker),
                       checklist (default deterministic checks),
                       curiosity (4 gap-detectors + ComposeChecklists),
                       trust (TrustStore), gate.go (ClaudeCodeGate routes
                       risky claude_code__* to mem_trust_contracts),
                       http (4 endpoints)
    voyager/           manager (state, OnSkillPromoted callback for
                       procedural-memory writes),
                       extractor (SessionEnd → SKILL.md draft),
                       source_extractor (SessionEnd → mem_code_proposals
                       refactor draft from file-fight patterns), verifier
                       (synthetic tests via Haiku), discovery (tool-triplet
                       patterns),
                       optimizer.go (GEPA sidecar, hard gates, persists
                                     WHOLE Pareto frontier with
                                     frontier_run_id + pareto_rank;
                                     SampleFromFrontier for runtime A/B),
                       autotrigger.go (background ticker — watches
                                       mem_skill_runs, auto-fires GEPA
                                       on failure-rate threshold),
                       api.go (/status, /proposals, /code-proposals, /optimize)
    cron/              types, scheduler (robfig/cron/v3),
                       executor_agent (cron→agent.Loop bridge), http
    sentinel/          types, manager, dispatcher (Log + Skill), http
    server/            server, health, ws, api, memory_api, audit_api, gym_api

    --- the assembly substrate (§18) — six generic building-block packages ---
    surface/           generic dashboard surface contract — types, store;
                       backs surface_item / surface_update tools
    workflow/          durable workflow engine — types, store, engine
                       (background worker, retries, checkpoints, resume),
                       validate (static step-list check)
    extensions/        runtime self-extension — types, store, http_tool
                       (generic REST tool), manager, tools (extension_*)
    eval/              verification substrate — eval.go (Store + Scorecard
                       with regression detection), tools (eval_*)
    worldmodel/        world model + agent goals — types, store
                       (entities + links + goals), tools (entity_* / goal_*)
    initiative/        initiative + economics — initiative.go (notification
                       + cost ledgers, Notifier urgency policy), tools
                       (notify / notification_digest / cost_record /
                       budget_status)
    plasticity/        Gym substrate read store for training examples,
                       distillation datasets/runs, adapters, adapter evals,
                       and policy routes
    proactive/agent_goals.go + substrate_surface.go — heartbeat checklists
                       that pursue agent goals + mirror substrate state onto
                       the surface contract
    cmd/infinity/workflow_executor.go + initiative_deliverer.go — the
                       concrete Executor / Deliverer adapters (package main,
                       so the substrate packages stay dependency-light)

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
    gym/               Gym: plasticity status + datasets/adapters/routes +
                       combined audit ledger
    audit/             redirects to /gym?tab=audit
    settings/          Phase 2 MVP
  components/
    TabFrame.tsx       sticky header (logo + StatusPill + TabNav + ThemeToggle)
                       + main + FooterStatus
    TabNav.tsx         primary tabs: dashboard / live / memory / gym / skills
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
| `/api/memory/reflections?limit=` | GET | server.handleMemoryReflections | recent metacognitive critiques |
| `/api/memory/predictions?threshold=&limit=` | GET | server.handleMemoryPredictions | high-surprise predict-then-act rows |
| `/api/memory/cite/:id` | GET | server.handleMemoryCite | provenance chain |
| `/api/memory/audit?limit=&op=` | GET | server.handleAuditLog | mem_audit rows (table#id target) |
| `/api/gym?limit=` | GET | server.handleGym | Gym snapshot: plasticity ledgers + policy routes |
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
| `/api/voyager/code-proposals?status=` | GET | voyager.API | list mem_code_proposals (source-extractor output) |
| `/api/voyager/code-proposals/:id/decide` | POST | voyager.API | approve / reject / applied (with optional note) |

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

The IntentFlow detector, WAL, and Working Buffer are wired into the WebSocket handler. Each user turn records durable session-state fragments, classifies the turn asynchronously, emits an `intent` WS frame for Studio, and appends completed turn pairs to the working buffer for compaction recovery.

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

## 12. Voyager / GEPA — skill self-evolution + code self-noticing

Three coordinated loops, on by default — disable explicitly via
`INFINITY_VOYAGER=false`.

### Voyager creation (`core/internal/voyager/`)
- **Discovery** — every PostToolUse appends to a per-session window of tool
  names. Repeated N-tuples across sessions become candidates in
  `mem_skill_proposals`.
- **Extraction** — on SessionEnd, score recent observations against a
  heuristic. Above threshold → Haiku drafts a SKILL.md candidate.
- **Verification** — Haiku generates synthetic test cases. Instruction-only
  skills auto-promote; impl-bearing skills wait for human decide.

### Source extraction — code self-noticing (`source_extractor.go`)
The fourth Voyager hook. Counterpart of skill extraction, but for *source*
struggle instead of *behavior* crystallization.

- Hook: `OnSessionEndSource` registered on `SessionEnd` alongside
  `voyager.extract`.
- Heuristic (per session, scan ≤200 observations):
    - For each `claude_code__edit` / `claude_code__write` PreToolUse, pull
      `file_path` from the input.
    - Attribute the *next* `PostToolUseFailure` back to the last file the
      agent was editing (or count it as a bash failure if the last tool was
      `claude_code__bash`).
    - Flag as a "fight" any file with ≥3 edits AND either ≥1 attributed
      failure OR ≥1 session-wide bash failure (typecheck/build/test).
- Up to 3 hot files per session draft proposals (async, 120s Haiku window).
- Each draft asks Haiku for `{title, rationale, proposed_change,
  risk_level}` and lands a row in `mem_code_proposals` with full
  evidence JSON (edit count, failure count, bash sample, duration).
- LLM-less degradation: insert a stub row that surfaces the signal without
  the drafted change.

**Safety posture (load-bearing):** approving a code proposal does *not*
auto-apply the change. The `mem_code_proposals.status` column is intent
only — the agent attempts the edit on its next relevant turn, and that
edit still routes through `ClaudeCodeGate` → `mem_trust_contracts` → boss
approval per call. There is no autonomous write path to Go/Next.js source.
Voyager is doing autonomous *noticing*; the human still owns the keyboard.

### GEPA improvement (`core/internal/voyager/optimizer.go` + `docker/gepa/`)
Genetic-Pareto prompt evolution for *existing* skills. Per Agrawal et al.,
ICLR 2026 Oral ([arXiv 2507.19457](https://arxiv.org/abs/2507.19457)) —
reflective prompt evolution outperforms RL with 35× fewer rollouts when you
maintain a Pareto frontier and sample stochastically instead of locking to a
single champion.

```
POST /api/voyager/optimize { "skill": "<name>" }   ← manual entry
                                                   ← OR fired by AutoTrigger
   → voyager.RunOptimizer:
       1. pull recent mem_skill_runs for the skill (failures + successes)
       2. POST docker/gepa/ sidecar at /optimize with traces + current SKILL.md
          GEPA flow:
            • Haiku reads failure traces → root-cause summary (1 call)
            • Haiku mutates SKILL.md targeted at the root cause (6 candidates)
            • Haiku scores each candidate against eval cases (6 calls)
            • returns Pareto-sorted by score, size
       3. paretoFrontier applies hard gates (per candidate):
            • non-empty after trim
            • ≤15KB (mirror Hermes Phase 1 ceiling)
            • starts with "---" frontmatter
            • not byte-identical to original
            → returns ALL viable candidates, sorted by score desc
       4. assign a frontier_run_id (UUID); insert EVERY surviving candidate
          into mem_skill_proposals with score + pareto_rank + gepa_metadata
       5. boss reviews the frontier and promotes the winning rank(s) via
          /api/voyager/proposals/:id/decide
       6. SampleFromFrontier(skill) — draws weighted-by-score for runtime
          A/B (used by agent code that wants a non-champion variant on a
          specific call without permanent promotion)
```

The sidecar (`docker/gepa/`) holds no state — it just runs Haiku calls and
returns candidates. Hard gates ride on the Go side so a sidecar
compromise can't bypass them. Cost per run: ~$0.05–$0.20.

### Autotrigger (`core/internal/voyager/autotrigger.go`)
This is the close-the-loop step Voyager was missing. A background ticker
watches `mem_skill_runs` and auto-fires `RunOptimizer` for skills past the
failure threshold. Without this, GEPA only ran when someone POSTed
`/api/voyager/optimize` by hand.

```
voyager.NewAutoTrigger(manager, NewOptimizer())
  Enabled() == optimizer.Enabled() && env != INFINITY_VOYAGER_AUTOTRIGGER=off
  Start(ctx) → goroutine ticker at INFINITY_VOYAGER_AUTOTRIGGER_EVERY (default 30m)

every tick:
   SELECT skill_name, fails, total
     FROM mem_skill_runs (last 24h, last N runs per skill where N=MIN_RUNS)
    HAVING fails/total >= INFINITY_VOYAGER_FAILURE_RATE (default 0.30)

   for each failing skill:
      if last fire < INFINITY_VOYAGER_COOLDOWN (default 6h ago): skip
      RunOptimizer(skill, traceLimit=20)
      log frontier_run_id + candidates + calls
```

Tunables (all env): `INFINITY_VOYAGER_AUTOTRIGGER` (on|off), `_EVERY`,
`_FAILURE_RATE`, `_MIN_RUNS`, `_COOLDOWN`. Defaults: 30m, 0.30, 5, 6h.

## 13. AGI loops — migration 011

Migration `011_agi_loops.sql` adds the substrate for six AGI-trajectory
loops on top of the Phase 4-7 foundation. Each is grounded in 2024-2026
research, not vibes. The substrate is in place and runs automatically —
no env flag required (procedural memory, predictions, A-MEM, sleep-time,
curiosity all activate as soon as the binary boots against the migrated
DB). GEPA Pareto + autotrigger require `GEPA_URL` to be set.

### 13.1 Procedural memory tier (CoALA)

CoALA ([Sumers et al., arXiv 2309.02427](https://arxiv.org/abs/2309.02427))
names four memory tiers: working, episodic, semantic, procedural. The
schema has always had a `tier` column with all four values; we now actually
*populate* the procedural row when a skill is promoted, and *retrieve*
them through the same RRF machinery as semantic facts.

```
voyager.Manager.Decide(promote)
   → persistSkillToDB (existing path: mem_skills + versions + active)
   → writeSkillToDisk
   → m.onPromoted(ctx, name, description, skillMD)        ← NEW callback
       fired in serve.go to:
       memory.ProceduralStore.UpsertFromSkill
         → builds compact body (description + "When to use" section + first
           N step lines, capped at 1200 chars)
         → embeds title + body via embedder
         → INSERT/UPDATE mem_memories WHERE
              tier='procedural' AND title='skill:<name>'
              strength=1.0, importance=7 (above average by default)

memory.Searcher.BuildSystemPrefix(query)
   → fetchBossProfile()                                  ← unchanged
   → procedural.TopK(query, 5)                           ← NEW
       cosine search against tier='procedural' active rows
       (with query="" falls back to strength × importance ranking)
   → memory.FormatForPrompt(entries) writes:
       "## Your procedural skills (top matches)
        - skill_name — first line of description
        - ..."
   → standard RRF Search (unchanged) appends below
```

### 13.2 Reflection / metacognition

Park et al.'s Generative Agents (2023) reflection trees + Multi-Agent
Reflexion ([MAR, arXiv 2512.20845](https://arxiv.org/html/2512.20845v1))
critic-persona separation. The key MAR finding: the model that just acted
can't critique itself in the same call. We instantiate a separate critic
persona via a fresh Haiku call.

```
infinity reflect [--session <id> | --window 24h --limit 20] [--force]
   → memory.Reflector.ReflectOnSession / ReflectRecent
       1. pull observation transcript for the session(s)
          (≤60 obs, 12k chars; USER/ASSISTANT/TOOL annotated)
       2. llm.AnthropicCritic.CritiqueSession (Haiku, fresh persona via
          critiqueSystem prompt; strict JSON return shape)
       3. memory.Reflector.persist
          • INSERT mem_reflections (critique, lessons jsonb,
            quality_score, importance, embedding)
          • importance = 8 - 5*quality_score (low quality → HIGH importance:
            bad sessions are the ones worth remembering)
          • for each lesson with confidence >= 0.6:
              INSERT mem_lessons (lesson_text, confidence)

Reflections feed:
  - the curiosity scanner (low-quality reflections seed gaps)
  - mem_lessons (existing search machinery)
  - manual review in Studio (planned tab)
```

Cost: ~$0.01 per session at Haiku rates. The CLI command can also run
under cron for nightly reflection passes.

### 13.3 Predict-then-act (JEPA epistemic discipline)

No production text world model exists today, but the *posture* is
implementable: before non-trivial tool calls, emit an expected outcome;
after the call, score how surprising the actual result was. The delta
becomes a curriculum signal.

```
hooks.PredictionRecorder.Register(pipeline) wires:
  PreToolUse hook:
    extractToolMeta(payload) → (name, tool_call_id, input)
    expected = heuristicPrediction(name, input)         ← zero LLM cost
    store.Record(session, call_id, name, expected, input)
      → INSERT mem_predictions

  PostToolUse / PostToolUseFailure hook:
    extractToolMeta → tool_call_id
    actual = payload.output
    matched, surprise = memory.SurpriseFor(expected||name||input, actual)
       (Jaccard on token sets, lowered tokens, ≥3 chars)
       (error/blocked prefix on actual → strong surprise unless expected)
    failure hook → force matched=false, surprise=max(0.5, surprise)
    store.Resolve(call_id, actual, matched, surprise)
      → UPDATE mem_predictions SET resolved_at = NOW()
```

The agent loop emits `tool_call_id` on both Pre/Post payloads
(loop.go:326 + 419) so the recorder can pair them. The Jaccard heuristic
is intentionally crude — the *value* is the post-hoc score, not the
prediction text. High-surprise rows (`surprise_score >= 0.8`) feed the
curiosity scanner.

### 13.4 A-MEM auto-linking

A-MEM ([arXiv 2502.12110](https://arxiv.org/pdf/2502.12110), 2×
LoCoMo/MemGPT on multi-hop) — at write time, link the new memory to its
k nearest neighbours so retrieval can traverse the graph, not just rank.

```
compressor.Compress(observation, project) → mem_memories row (memID)
   → tx.Commit()
   → autoLinkNeighbours(memID, embedding) ← async goroutine
       SELECT id, 1 - (embedding <=> $1) AS sim
         FROM mem_memories
        WHERE id != $memID AND status = 'active' AND embedding IS NOT NULL
        ORDER BY embedding <=> $1 ASC
        LIMIT 4

       for each hit where sim >= 0.65:
         INSERT mem_relations (source_id, target_id, relation_type, confidence)
           VALUES ($memID, $hit_id, 'associative', $sim)
           ON CONFLICT DO NOTHING
```

Async so it never blocks the compression path. Bounded at k=4 with a
0.65 cosine floor to prevent the "everything is loosely related" noise
A-MEM's authors warn about. Pruned aggressively by sleep-time (§13.5)
to keep edge counts reasonable.

### 13.5 Sleep-time consolidation (8-op)

LightMem ([arXiv 2510.18866](https://arxiv.org/html/2510.18866v1))
argues consolidation is a distinct compute regime, not a cron job
afterthought. `ConsolidateNightly` is now an 8-op pipeline:

```
infinity consolidate [--compress] [--dry-run]
   → memory.ConsolidateNightly:
       1. Decay strength × 0.95 on all active memories
       2. Reset hot memories (last_accessed > 7d ago) to strength=1.0
       3. Cluster identification (episodic, cosine > 0.85, group of ≥3)
       4. Contradiction resolution:
          SELECT pairs FROM mem_relations WHERE relation_type='contradicts'
          for each pair where both are still active:
            mark the OLDER memory superseded; set superseded_by to the newer
       5. Associative pruning: keep top-10 outgoing 'associative' edges per
          (source, relation_type); delete the rest (rank by confidence desc)
       6. Weak-edge purge: DELETE 'associative' edges WHERE confidence < 0.40
       7. Procedural re-weight:
          For each skill with ≥3 runs in the last 7d:
            UPDATE mem_memories
               SET strength = LEAST(1.0, GREATEST(0.1, success_rate))
             WHERE tier='procedural' AND title='skill:<name>'
       8. RunAutoForget (decay-driven deletion of unimportant low-strength
          memories past the TTL)
   → returns ConsolidateReport JSON
```

The procedural re-weight in step 7 is the load-bearing one: skills that
fail get demoted in the procedural-tier ranking the system prompt pulls
from, so the agent stops reaching for habits that don't work.

Pair with `--compress` to also LLM-promote uncompressed observations
into the episodic tier (with A-MEM auto-linking firing on each new
memory).

### 13.6 Curiosity gap-scan

The proactive engine's heartbeat used to be purely time-triggered with a
deterministic checklist. We now compose `DefaultChecklist` with
`CuriosityChecklist` so every tick also scans for *what the agent
doesn't know*.

```
proactive.NewCuriosityScan(pool) provides 4 detectors:

  scanLowConfidence    SELECT semantic memories WHERE strength < 0.35
                       AND created_at < NOW() - 24h
                       → question = "Is this still true: ...?"
                       → source_kind='low_confidence'

  scanContradictions   SELECT pairs FROM mem_relations
                       WHERE relation_type='contradicts'
                       AND both endpoints status='active'
                       → question = "Two memories disagree — which is right?"
                       → source_kind='contradiction', importance=8

  scanUncoveredMentions SELECT graph_nodes with ≥3 obs but 0 derived memories
                       → question = "You've mentioned <type> <name> — what's
                                     important about it?"
                       → source_kind='uncovered_mention'

  scanHighSurprise     SELECT predictions WHERE surprise_score >= 0.8
                       AND resolved_at > NOW() - 48h
                       → question = "Tool <name> returned something
                                     unexpected — should I rework prompt?"
                       → source_kind='high_surprise'

All four insert into mem_curiosity_questions with ON CONFLICT DO NOTHING
guarded by a unique index on (question) WHERE status='open' — idempotent
across heartbeats.

proactive.CuriosityChecklist(pool) is a Checklist function that:
  1. runs scanner.Run(ctx) (writes new questions)
  2. queries scanner.ListOpen(5) (top-K by importance)
  3. returns each as Finding{Kind:"curiosity", Title:question, Detail:rationale}

ComposeChecklists(DefaultChecklist, CuriosityChecklist) is what's wired
in serve.go's heartbeat construction.
```

Open questions show up alongside `pattern`/`outcome`/`self_heal`/`security`
findings in `mem_heartbeat_findings`. The Studio Heartbeat tab renders
them with the curiosity kind badge.

## 14. Studio conventions

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

## 15. Deployment

```
Railway project: Infinity
  ├─ core            rootDirectory=core/            golang:1.26-alpine → distroless
  ├─ studio          rootDirectory=studio/          node:22-alpine → standalone
  │                  custom domain: infinity.dopesoft.io (CNAME → studio.up.railway.app)
  ├─ gepa            rootDirectory=docker/gepa/     python:3.12 + FastAPI + httpx
  ├─ plasticity      rootDirectory=docker/plasticity/ python:3.12 stdlib HTTP worker
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
| `GEPA_URL` | core | `http://gepa.railway.internal:8080` — also enables the Voyager autotrigger when set |
| `PLASTICITY_URL` | core | `http://plasticity.railway.internal:8080` — future Gym worker endpoint for dataset build / adapter train / eval jobs |
| `INFINITY_VOYAGER_AUTOTRIGGER` | core | `on` (default when `GEPA_URL` set) / `off` |
| `INFINITY_VOYAGER_AUTOTRIGGER_EVERY` | core | Go duration; default `30m` |
| `INFINITY_VOYAGER_FAILURE_RATE` | core | float 0..1; default `0.30` (fire GEPA when failure rate ≥ this) |
| `INFINITY_VOYAGER_MIN_RUNS` | core | int; default `5` (sliding window size per skill) |
| `INFINITY_VOYAGER_COOLDOWN` | core | Go duration; default `6h` (per-skill GEPA cooldown) |
| `DB_CONNECTION_URI` | honcho, honcho-deriver | typically `${{core.DATABASE_URL}}`; CMD rewrites scheme |
| `CACHE_URL` | honcho, honcho-deriver | `redis://default@redis.railway.internal:6379/0?suppress=true` |
| `AUTH_USE_AUTH` | honcho, honcho-deriver | `false` (Railway private network is the perimeter) |
| `NEXT_PUBLIC_CORE_URL` | studio | https origin of core service (baked at build time) |
| `NEXT_PUBLIC_CORE_WS_URL` | studio | wss origin + `/ws` path |

## 16. Phase status (honest)

| Phase | Spec scope | What's live | Gaps |
|---|---|---|---|
| 0 | Repo + CLI + health + studio shell | ✅ all | — |
| 1 | Working text bot | ✅ all | — |
| 2 | Tools + MCP + Settings MVP | ✅ all | Settings tab depth — Phase 7 |
| 3 | Memory subsystem | ✅ all | Recall@10 benchmark fixture pending |
| 4 | Skills system | ✅ schema, registry, process-jail, agent tools, HTTP API, Studio Skills tab | Container sandbox for high/critical risk · network egress enforcement at the HTTP transport · Tests sub-tab in Studio · "+ New skill" / Import buttons · Edit + Disable + dropdown export/fork/archive |
| 5 | Proactive engine | ✅ IntentFlow detector, WAL, Working Buffer, Heartbeat, Trust queue, all schemas, HTTP APIs, Heartbeat + Trust Studio tabs; ✅ **Curiosity gap-scan** composed into heartbeat (low-confidence / contradictions / uncovered mentions / high-surprise predictions); ✅ IntentFlow + WAL + Buffer auto-fired from the WS handler | **Hierarchical memory access** struct (DepthFor exists; not wired) · **Compaction Recovery** flow on session start · Heartbeat checklist items: security scan, memory % · Live tab 3-column layout · Studio Heartbeat sub-tabs (Proactive tracker, Pattern recognition, Outcome journal, Curiosity loop, Surprise queue) · Phase 5 Studio components: ControlTokenBadge, IntentStream, ContextBudget, SuggestionCard, TrustGate · "Always allow this pattern" rules · bulk approve in Trust |
| 6 | Voyager + Cron + Sentinels | ✅ Cron scheduler + Sentinel manager + Skill dispatcher + schemas + HTTP APIs + Studio Cron+Sentinels tab; ✅ **Voyager source extractor**; ✅ **GEPA Pareto frontier persistence** (per ICLR 2026 Oral); ✅ **Voyager autotrigger** — closes the failure→curriculum→skill→optimization cycle by auto-firing GEPA on skills past the failure threshold. | **Curriculum** generator · **Skill generator** (LLM-driven) · **Verifier** synthetic tests · **AutoSkill** failure-reflection-patch loop · Skill discovery hooks (regex pattern detection in observations) · Sentinel runtimes for non-webhook watch types (file_change, memory_event, external_api_poll, threshold) · Skills tab Candidate column population · Studio frontier-comparison view (render Pareto siblings side-by-side) · NaturalLanguageScheduleInput live parser · Verification log sub-tab · Auto-apply path for approved code proposals |
| 7 | Polish | ✅ Audit log endpoint + viewer; ✅ Honcho dialectic peer modelling; ✅ Claude Code coding bridge (25 tools via MCP + CF Access); ✅ GEPA skill optimizer sidecar; ✅ custom domain `infinity.dopesoft.io` | Command palette (cmd+K, cmdk lib) · Sessions rewind · Skills Tests sub-tab · Settings 10-section depth · Memory tab knowledge graph viewer · Backup/export · `infinity restore` · Doctor full diagnostic suite · Light/dark + animation polish |
| **AGI** | **Migration 011 — close the AGI loops** | ✅ **Procedural memory tier (CoALA)** — promoted skills materialize as `tier='procedural'` rows; ✅ **Reflection / metacognition** — `infinity reflect` + `mem_reflections` (MAR critic persona); ✅ **Predict-then-act** — `mem_predictions` paired Pre/Post with Jaccard surprise scoring; ✅ **A-MEM auto-linking** — top-4 'associative' edges at compress time; ✅ **Sleep-time consolidation** — 8-op nightly regime with contradiction resolution + edge pruning + procedural reweight; ✅ **Curiosity scanner** integrated into heartbeat; ✅ Studio Memory feeds for reflections + high-surprise predictions; ✅ curiosity approval / dismissal in Heartbeat; ✅ procedural-tier badge in Memory list | A-MEM graph visualization for top-K associative neighbours · LLM-driven prediction text on high-cost tool calls (Haiku, gated on cost heuristic) · Cross-session reflection chains (cluster N reflections → meta-lesson) |
| **Substrate** | **Migrations 016–021 — the assembly substrate (§18)** | ✅ **Generic surface contract** (`mem_surface_items` + `surface_item`/`surface_update`); ✅ **Skill self-authoring loop** (`skill_create` → live registry, durable across restarts); ✅ **Durable workflow engine** (`mem_workflows`/`_runs`/`_steps` + background worker — retries, checkpoints, resume-on-restart, dependency-aware scheduling); ✅ **Runtime self-extension** (`mem_extensions` — agent wires MCP servers + REST-API tools live); ✅ **Verification** (`mem_evals` scorecards with regression detection + `workflow_validate`); ✅ **World model + agent goals** (`mem_entities`/`_links`/`mem_agent_goals` + autonomous-pursuit heartbeat); ✅ **Initiative + economics** (`mem_notifications` urgency policy + `mem_cost_events` budget rollup) | Sandboxed dry-run execution of workflows · automatic per-LLM-call cost capture · multi-dependency (DAG) workflow scheduling · full-registry browse views in Studio (extensions / evals / entities are agent-tool-queryable today) |
| **Gym** | **Migration 022 — plasticity control surface** | ✅ Plasticity metadata schema (`mem_training_examples`, datasets, runs, adapters, adapter evals, policy routes); ✅ `core/internal/plasticity` read store; ✅ deterministic example extraction from evals/reflections/high-surprise predictions; ✅ prompt-path reflex provider injects top Gym lessons into the agent through `CompositeMemory`; ✅ `/api/gym` + POST extract action; ✅ `infinity gym extract`; ✅ Studio `/gym` page with overview, datasets, adapters, routes, and combined audit ledger; ✅ `/audit` redirects to Gym audit tab; ✅ `docker/plasticity` Railway sidecar skeleton | Nightly extraction scheduling · sidecar train/eval implementation · eval replay / adapter promotion Trust contracts · learned policy router integration · object-storage artifact backend |
| 8 | Voice | ✅ **GPT Realtime over WebRTC** — `core/internal/voice/realtime.go` mints short-lived OpenAI `client_secret`s; the browser does the WebRTC SDP exchange P2P with `api.openai.com` (audio never touches Core); tool calls round-trip through `/api/voice/tool` so voice has the same registry + Trust gate as text, and `/api/voice/turn` fires the same memory-capture hooks. Model `gpt-realtime-1.5`, server-VAD barge-in, live transcription. nil-safe — `/api/voice/*` returns 503 when `OPENAI_API_KEY` is unset. | Studio mic-button polish · wake-word activation (currently tap-to-talk) |

## 17. Next-session priorities

The AGI-loop substrate (migration 011) is in place — every loop runs
automatically once the binary boots. The next layer of work is mostly
Studio surfaces + scheduling polish:

1. **Finish AGI-loop Studio depth.** `/memory` now has Reflections and
   Predictions feeds, `/heartbeat` can approve/dismiss curiosity questions,
   and procedural memories carry their tier badge. Remaining surface work:
   A-MEM graph visualization for top-K associative neighbours and
   cross-session reflection chains.
2. **Schedule the loops via cron.** `infinity reflect` and `infinity
   consolidate` should both run nightly. Either set a Railway cron job
   or add a `mem_crons` row pointing at the existing `agent.Loop`-driven
   isolated-turn target. The Voyager autotrigger is the only loop that's
   wired to its own goroutine; the other two are CLI-invoked.
3. **Compaction Recovery.** On session start, if the user message
   contains a `<summary>` tag or matches "where were we", read
   `mem_working_buffer` + `mem_session_state` and surface a "recovered
   from buffer" message.
4. **Container sandbox.** `core/internal/skills/sandbox_container.go`
   with `docker/docker/client`. Unblocks high/critical-risk skills.
5. **Pareto frontier comparison UI.** Studio render of N candidates
   sharing a `frontier_run_id` side-by-side, with promote/reject per
   row. Backed by the existing `/api/voyager/proposals?status=candidate`
   endpoint — add a `?frontier=<id>` filter.
6. **LLM-driven prediction text** for high-cost tool calls. The current
   `heuristicPrediction` is rule-based and free; a per-call Haiku call
   gated on a difficulty heuristic would sharpen the surprise signal on
   the calls that matter most.

Phases 4-7 + AGI substrate is feature-complete enough that each gap
above is a focused, scoped follow-up — no architectural rework needed.

---

## 18. The assembly substrate (migrations 016–021)

The principle is **Rule #1** in [CLAUDE.md](CLAUDE.md): the agent *assembles*
workflows from natural language out of generic building blocks; Go is for
the substrate, never the cognition. The full per-phase trail — schema,
data flow, code map — lives in [`docs/substrate/README.md`](docs/substrate/README.md).
This section is the architectural summary.

Six packages, six migrations, ~26 agent tools. Each phase is a generic,
schema-driven contract — not a feature:

| # | Package | Migration | Contract |
|---|---|---|---|
| 1a | `surface/` | `016` | `mem_surface_items` — one generic table any producer writes ranked, structured items into; Studio's `SurfaceCard` renders any `surface` key. Tools: `surface_item`, `surface_update`. |
| 1b | `skills/` (extended) | — | `skill_create` + `Registry.Put` — a low-risk recipe the agent authors goes **live this session**, persisted to `mem_skills`, re-hydrated on boot. |
| 2 | `workflow/` | `017` | `mem_workflows`/`_runs`/`_steps` + a background `Engine` — claims a runnable run, advances one step per tick (`tool`/`skill`/`agent`/`checkpoint`), persists after every step, retries with attempts, resumes mid-flow on restart (`ReclaimOrphans`). Tools: `workflow_create/_run/_status/_resume/_cancel/_list/_validate`. |
| 3 | `extensions/` | `018` | `mem_extensions` — the agent wires an MCP server or a REST-API-as-tool at runtime; live this session, re-activated on boot via `Manager.LoadAll`. Tools: `extension_register/_list/_remove`. |
| 4 | `eval/` | `019` | `mem_evals` outcome ledger + `Scorecard` (success rate, recent-vs-prior trend, regression flag). The workflow engine auto-records every run. Tools: `eval_record`, `eval_scorecard`. |
| 5 | `worldmodel/` | `020` | `mem_entities`/`_links` (structured model of the boss's world) + `mem_agent_goals` (the agent's own objectives, living plan). Heartbeat `AgentGoalChecklist` resurfaces stalled goals. Tools: `entity_upsert/_link/_get/_search`, `goal_set/_update/_list`. |
| 6 | `initiative/` | `021` | `mem_notifications` (urgency-routed: push / surface / digest) + `mem_cost_events` (budget rollup vs `INFINITY_BUDGET_USD`) + `mem_workflow_runs.depends_on` (dependency-aware scheduling). Tools: `notify`, `notification_digest`, `cost_record`, `budget_status`. |

**Dependency discipline.** Each substrate package imports only `pgx` +
stdlib (+ `tools` for the packages that register tools). Cross-subsystem
wiring — the workflow `Executor`, the `CheckpointSurfacer`, the
`EvalRecorder`, the initiative `Deliverer` — is done via interfaces, with
the concrete adapters living in `cmd/infinity/` (package main) so the
substrate packages stay dependency-light. Same pattern as `cron`'s
`CronScheduler` interface.

**Studio.** No new top-level pages. The generic surface contract (1a)
renders five surface groups through one `SurfaceCard`: `followups`,
`agenda` (the agent's goals — distinct from the boss's Pursuits card),
`health` (broken extensions + regressed capabilities, mirrored by
`SubstrateSurfaceChecklist`), `alerts`, `approvals`. Workflow runs flow
through the existing Agent Work board — tapping a Kanban card opens the
ObjectViewer drawer with the run's step state-machine inline.

**Boot wiring** is in `cmd/infinity/serve.go`: surface/workflow/eval/
worldmodel tools register in the memory block; the workflow `Engine`
starts after the agent loop exists; `extensions.Manager.LoadAll` runs
after the embedded `mcp.yaml` connect; the `initiative` tools register
after the push `Sender` is built. The heartbeat composes
`DefaultChecklist + CuriosityChecklist + AgentGoalChecklist +
SubstrateSurfaceChecklist`.
