# Infinity

**Your always-on AI with a real memory, real hands, and a real model of who you are.**

Not a chatbot. Not a wrapper. A permanent cognitive substrate that remembers everything, codes on your home Mac, learns from its own failures, and gets sharper the longer you use it.

> Hermes lights up on demand. Nanobot keeps a tiny loop. OpenClaw runs skills.
> Infinity does all three — plus codes on your Max subscription via MCP, models *who you are* with Honcho, evolves its own skills with GEPA, and audits every action it takes.

---

## What's new this week

**Closed the AGI loops.** Migration `011_agi_loops.sql` + nine landings turn the substrate into a system that genuinely *learns continuously* — not just stores. Each move is grounded in 2024-2026 research, not vibes:

- **🧬 GEPA Pareto frontier** ([arXiv 2507.19457](https://arxiv.org/abs/2507.19457), ICLR 2026 Oral) — the optimizer now persists the **whole frontier** of skill variants (not a single champion) with `frontier_run_id` + `pareto_rank`. `SampleFromFrontier` draws weighted by score. 35× rollout efficiency vs. RL.
- **🚨 Voyager autotrigger** — background ticker watches `mem_skill_runs`. When a skill's recent failure rate crosses 30%, GEPA fires automatically. **The close-the-loop step Voyager originally promised but never had.** Per-skill cooldown (6h default).
- **🎯 Procedural memory tier activated** — promoted skills materialize as `tier='procedural'` rows with embeddings. Retrieved through the same RRF machinery as semantic facts and injected into every system prompt. CoALA's procedural tier ([arXiv 2309.02427](https://arxiv.org/abs/2309.02427)), populated.
- **🪞 Metacognition / reflection** — `infinity reflect` walks recent sessions, runs a fresh "critic persona" Haiku call (Multi-Agent Reflexion, [arXiv 2512.20845](https://arxiv.org/html/2512.20845v1)), and writes structured critique + lessons to `mem_reflections`. High-confidence lessons auto-promote into `mem_lessons`.
- **🔮 Predict-then-act** — every PreToolUse writes an expected outcome; PostToolUse resolves with a Jaccard surprise score (`mem_predictions`). High-surprise rows feed the curiosity scanner. JEPA epistemic discipline without a generative world model.
- **🕸️ A-MEM auto-linking** ([arXiv 2502.12110](https://arxiv.org/pdf/2502.12110)) — when the compressor promotes an observation, it also writes top-4 cosine-nearest `'associative'` edges into `mem_relations`. Retrieval can now traverse the graph, not just rank by score.
- **💤 Sleep-time consolidation** ([LightMem, arXiv 2510.18866](https://arxiv.org/html/2510.18866v1)) — `infinity consolidate` rebuilt as an 8-op offline regime: decay → hot-reset → cluster → **contradiction resolution** → **associative pruning** → **weak-edge purge** → **procedural re-weight from success rates** → forget. Distinct from online compression.
- **❓ Curiosity gap-scan** — the heartbeat now scans memory for **low-confidence nodes**, **unresolved contradictions**, **uncovered graph mentions**, and **high-surprise predictions**, then writes them as `mem_curiosity_questions`. The proactive engine finally *finds gaps*, not just reacts to time.

Prior landings still apply:

- **🛠️ Claude Code coding bridge** — 25 tools (Bash, Edit, Write, Read, Grep, Glob, Agent, WebFetch, …) live in your agent via MCP-over-SSE through Cloudflare Tunnel to your home Mac. Runs under your **Max subscription** (no API tokens billed for code work). ToS-clean — OAuth tokens never leave the Mac. Every dangerous call queues in the Trust queue for one-tap approve from your phone.
- **🧠 Honcho dialectic peer modelling** — runs alongside the 12-table mem store. Two new Railway services (FastAPI server + deriver worker) reason continuously about *who the boss is* and fold a peer representation into every system prompt. Privacy filter still runs at the capture boundary.
- **🔁 GEPA skill self-evolution** — Python sidecar (Hermes-class) for SKILL.md optimization. ~$0.05–$0.20 per cycle.

Plus: Studio is now reachable on the boss's **private domain** instead of the raw `*.up.railway.app` URL.

---

## What Infinity is

Infinity is your **always-on personal AI**, designed for one user — you — across every device. It's a cognitive layer, not a tool. Every conversation, every tool call, every observation becomes a memory. Every memory has a source. Every fact can be traced back to where it came from. Nothing is forgotten unless you tell it to forget. And when you tell it to code something, it does — on your hardware, with your subscription, gated by your approval.

Behind the scenes Infinity runs as **six Railway services** plus **Postgres + pgvector** plus a **home Mac**:

| Service | What |
|---|---|
| `core` | Go binary — agent loop, MCP client, memory, hooks, Trust gate, proactive engine |
| `studio` | Next.js 14 PWA — eight tabs, iOS-Safari-hardened, served on the boss's private domain |
| `gepa` | FastAPI sidecar — Genetic-Pareto SKILL.md optimizer (Hermes-class self-evolution) |
| `honcho` | FastAPI — dialectic peer modelling (continuous user model) |
| `honcho-deriver` | Background worker — refreshes peer reps from new traffic |
| `redis` | Honcho's cache + queue |
| **Mac (your house)** | `cloudflared` tunnel + `mcp-proxy` + `claude mcp serve` (Max sub) |
| **Database** | Postgres 16 + pgvector on Supabase session pooler |

---

## Feature comparison — Infinity vs Hermes vs OpenClaw vs Nanobot

Honest comparison after pulling the actual READMEs and code. ✅ = shipping, ⚠️ = partial / scaffolded / version-dependent, ❌ = absent. Hermes is [NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent). OpenClaw is [openclaw/openclaw](https://github.com/openclaw/openclaw). Nanobot is [nanobot-ai/nanobot](https://github.com/nanobot-ai/nanobot) (formerly acorn-io).

### How you use it day-to-day

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Web/PWA you can open on your phone | ✅ private domain | ❌ chat via channels | ⚠️ companion apps | ⚠️ localhost web UI |
| Custom domain at *your* address | ✅ | ❌ | ❌ | ❌ |
| Mobile-first UI hardened for iOS Safari | ✅ 100dvh, safe-area, sticky composer, WS reconnect | ❌ | ❌ | ❌ |
| Single, always-on installation | ✅ Railway | ✅ VPS / Modal | ✅ daemon | ✅ local |
| Talks to you in WhatsApp/Slack/Telegram | ❌ (not on roadmap) | ✅ 8+ channels | ✅ 22+ channels | ⚠️ via MCP-UI |
| Voice (wake word, dictation) | ❌ | ❌ | ✅ macOS/iOS/Android | ❌ |

### Memory (the moat)

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Remembers across sessions | ✅ 12-table relational + vector store | ✅ FTS5 + Honcho | ⚠️ MEMORY.md files | ❌ |
| Vector embeddings + ANN search | ✅ pgvector HNSW (384-dim) | ❌ FTS5 only | ❌ | ❌ |
| Triple-stream recall (BM25 + vector + graph) | ✅ Reciprocal Rank Fusion @ k=60 | ❌ | ❌ | ❌ |
| Session diversification (anti echo-chamber) | ✅ | ❌ | ❌ | ❌ |
| Provenance — every memory cites its source | ✅ `mem_memory_sources` chain | ❌ | ❌ | ❌ |
| Cascading staleness through the graph | ✅ `MarkSuperseded` | ❌ | ❌ | ❌ |
| Privacy filter at the capture boundary | ✅ `StripSecrets` 10-regex + tag-strip | ❌ | ❌ | ❌ |
| Audit log of every memory operation | ✅ `mem_audit` table + `/api/memory/audit` | ❌ | ❌ | ❌ |
| LLM-driven episodic compression (Haiku) | ✅ strict-JSON entity extraction | ❌ | ❌ | ❌ |
| **A-MEM auto-linking at write time** | ✅ top-4 cosine neighbours per new memory | ❌ | ❌ | ❌ |
| **Procedural memory tier (CoALA)** | ✅ promoted skills retrievable via RRF | ⚠️ skill registry only | ⚠️ skill files | ❌ |
| **Reflection / metacognition tier** | ✅ `mem_reflections` (MAR critic persona) | ❌ | ❌ | ❌ |
| **Predict-then-act with surprise scoring** | ✅ `mem_predictions` paired Pre/Post | ❌ | ❌ | ❌ |
| **Sleep-time consolidation (8-op)** | ✅ contradiction resolution + edge pruning + procedural reweight | ❌ | ❌ | ❌ |
| Nightly consolidation (decay + cluster + forget) | ✅ `infinity consolidate` | ❌ | ❌ | ❌ |
| Dialectic peer modelling (*who is the user?*) | ✅ Honcho integrated via `CompositeMemory` | ✅ Honcho built-in | ❌ | ❌ |

### Coding capability

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Edit local files | ✅ via Claude Code MCP (25 tools) | ✅ terminal backends | ✅ first-class | ⚠️ via MCP |
| Run shell commands | ✅ `claude_code__Bash` | ✅ | ✅ | ⚠️ via MCP |
| Multi-file refactors | ✅ Claude Code Agent | ✅ | ⚠️ | ⚠️ |
| Runs on user's **Max subscription** (not API tokens) | ✅ uniquely ToS-clean via `mcp serve` | ⚠️ API key required | ⚠️ API key required | ⚠️ API key required |
| Coding-session capture into memory | ✅ every tool call → `mem_observations` w/ provenance | ❌ | ❌ | ❌ |
| Per-call approval gate (human-in-the-loop) | ✅ `ClaudeCodeGate` → `mem_trust_contracts` | ❌ | ⚠️ allow/deny lists | ❌ |
| Approve from phone push notification | ✅ Studio Trust tab + deep-link from tool card | ❌ | ❌ | ❌ |
| Sub-agents / parallel work | ✅ `claude_code__Agent` exposed | ❌ | ❌ | ❌ |
| Run `pnpm test` / `pytest` from chat | ✅ via Bash tool | ✅ | ✅ | ⚠️ via MCP |

### Skills system

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Filesystem-native `SKILL.md` + YAML frontmatter | ✅ OpenClaw-compatible (drop-in symlink) | ✅ same lineage | ✅ original format | ❌ |
| Sandboxed by risk tier | ⚠️ process-jail live; container WIP | ✅ 7 backends (Docker, Modal, …) | ✅ Docker/SSH/OpenShell | ❌ |
| **Self-creation** — new skills from session patterns | ✅ Voyager extractor + verifier | ❌ | ❌ | ❌ |
| **Self-evolution** — existing skills get better | ✅ GEPA optimizer (size/score gates) | ✅ GEPA Phase 1 | ❌ | ❌ |
| **Pareto frontier persistence** (vs. single champion) | ✅ ICLR 2026 Oral pattern | ❌ | ❌ | ❌ |
| **Auto-trigger on failure rate** (close the loop) | ✅ ticker → GEPA on ≥30% failure | ❌ | ❌ | ❌ |
| **Self-noticing** — refactor proposals from file-fight patterns | ✅ Voyager source extractor → `mem_code_proposals` | ❌ | ❌ | ❌ |
| Skill versions + rollback | ✅ `mem_skill_versions` + `mem_skill_active` | ❌ | ⚠️ | ❌ |
| Run history with success rate | ✅ `mem_skill_runs` | ⚠️ | ⚠️ | ❌ |
| Trigger-phrase matching at turn-start | ✅ Token Jaccard, threshold 0.5 | ⚠️ | ✅ | ❌ |
| Public skill registry | ❌ (private skills/) | ✅ agentskills.io | ✅ ClawHub | ❌ |

### Autonomy (acts without being asked)

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Proactive heartbeat (agent ticks on its own) | ✅ 30-min checklist | ❌ | ⚠️ via cron | ❌ |
| **Curiosity gap-scan** (finds what it *doesn't* know) | ✅ low-confidence / contradictions / uncovered mentions / surprise | ❌ | ❌ | ❌ |
| Cron-scheduled agent runs | ✅ robfig/cron, prompt-as-target | ❌ | ✅ cron tool | ❌ |
| Webhook-triggered sentinels | ✅ live; file/memory/poll scaffolded | ❌ | ⚠️ | ❌ |
| Intent classifier per user turn (silent / fast / full) | ✅ Haiku-backed | ❌ | ❌ | ❌ |
| Working buffer for context recovery (60% threshold) | ✅ `mem_working_buffer` | ❌ | ❌ | ❌ |
| Outcome journal (tracks promises) | ✅ `mem_outcomes` | ❌ | ❌ | ❌ |
| Trust-Contract queue for risky actions | ✅ general primitive — gates skills, gates coding, gates evolved variants | ⚠️ PR review for evolved skills only | ⚠️ allow/deny lists | ❌ |

### Operations & platform

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| MCP client (consume other people's servers) | ✅ stdio + SSE, bearer/CF-Access auth | ✅ | ⚠️ MCP Registry | ✅ (this is its identity) |
| Multi-provider LLM | ⚠️ Anthropic primary, OpenAI/Google stubs | ✅ 200+ via OpenRouter | ✅ multi-provider | ✅ OpenAI/Anthropic/Azure/Bedrock/Ollama |
| Pluggable embedder (stub or HTTP sidecar) | ✅ | ❌ | ❌ | ❌ |
| Open source / self-hostable | ⚠️ private repo, self-hostable | ✅ MIT | ✅ | ✅ |
| One-bill deploy (Railway/cloud) | ✅ 6 services, all on Railway | ✅ Modal/VPS/Vercel | ✅ daemon | ✅ Homebrew |
| Health + doctor + diagnostics | ✅ `infinity doctor` | ⚠️ | ⚠️ | ⚠️ |
| Embedded migrations in the binary | ✅ `//go:embed` | ❌ | ❌ | ❌ |
| Distroless runtime image | ✅ | ❌ | ❌ | ❌ |

### Bottom line

| | One-sentence verdict |
|---|---|
| **Infinity** | The only one designed from day one as a permanent cognitive substrate that genuinely *learns* — memory-first, provenance-tracked, proactive, coding-capable on the user's subscription, **with closed AGI loops** (Pareto frontier skill evolution + auto-trigger on failure, reflection-tier metacognition, predict-then-act surprise scoring, A-MEM auto-linking, sleep-time consolidation, curiosity gap-scan) — on a mobile-first UI you can open on your phone. |
| Hermes | The closest peer — same SKILL.md lineage, broader sandbox + channel reach, shipping skill self-evolution. Differs by going lighter on memory engineering (FTS5 only, no provenance, no audit, single-champion skill optimization, no reflection or prediction tiers, no curiosity loop). |
| OpenClaw | The parent project of both Infinity's and Hermes's skill format. Strongest at channel reach (22+) and voice. Weaker on persistent memory and self-evolution. |
| Nanobot | A pure MCP host, not a memory product. Excellent if all you want is to plug MCP servers into a chat UI. No persistent memory, no skills, no autonomy. |

If you've already built skills for OpenClaw, **drop them into `./skills/` or symlink from `~/.openclaw/workspace/skills/<name>` and Infinity loads them unmodified.**

---

## Architecture map

```
                 ┌──────────────────────┐
   iPhone ─────► │  Studio (Next.js 14) │   <boss's private domain>
                 └──────────┬───────────┘   (CNAME via your DNS provider)
                            │ HTTPS + WSS
                            ▼
                 ┌──────────────────────────────────────────────────┐
                 │  Core (Go 1.26)                                  │
                 │  ┌──────────────┐    ┌────────────────────────┐ │
                 │  │ Agent loop   │◄──►│ Tool Registry           │ │
                 │  │ (small, nano-│    │  • native: fetch,search,│ │
                 │  │  bot-style)  │    │    code_exec, memory    │ │
                 │  │ + ToolGate   │    │  • MCP: filesystem      │ │
                 │  │ + Composite  │    │  • MCP: claude_code (25)│─┼──┐
                 │  │   Memory     │    └────────────────────────┘ │  │
                 │  └──────────────┘                                 │  │
                 │  ┌──────────────┐    ┌────────────────────────┐ │  │
                 │  │ Hooks (12)   │───►│ Memory Subsystem        │ │  │
                 │  │ + privacy    │    │ • RRF retrieval         │ │  │
                 │  │   filter     │    │ • Haiku compression     │ │  │
                 │  └──────────────┘    │ • provenance chain      │ │  │
                 │  ┌──────────────┐    │ • audit log             │ │  │
                 │  │ Proactive    │    └────────────────────────┘ │  │
                 │  │ (heartbeat,  │    ┌────────────────────────┐ │  │
                 │  │  trust queue,│───►│ Honcho Provider         │─┼──┼──┐
                 │  │  intent)     │    │ (dialectic peer model)  │ │  │  │
                 │  └──────────────┘    └────────────────────────┘ │  │  │
                 │  ┌──────────────┐    ┌────────────────────────┐ │  │  │
                 │  │ Cron+Sentinel│    │ Voyager + GEPA          │─┼──┼──┼──┐
                 │  │ (autonomy)   │    │ (skill self-evolution)  │ │  │  │  │
                 │  └──────────────┘    └────────────────────────┘ │  │  │  │
                 └────────────────────────┬─────────────────────────┘  │  │  │
                                          │ pgxpool                    │  │  │
                                          ▼                            │  │  │
                                Postgres + pgvector                    │  │  │
                                (Supabase session pooler)              │  │  │
                                                                       │  │  │
   ┌───────────────────────────────────────────────────────────────────┘  │  │
   │                                                                      │  │
   ▼  via Cloudflare Tunnel + Access service-token                        │  │
   Mac at home                                                             │  │
   • cloudflared (system daemon)                                           │  │
   • mcp-proxy 127.0.0.1:8765 (stdio↔SSE bridge)                          │  │
   • claude mcp serve (Max subscription, OAuth in Keychain)               │  │
                                                                          │  │
   ┌──────────────────────────────────────────────────────────────────────┘  │
   ▼ Railway internal network                                                │
   honcho (8000) + honcho-deriver (worker) + redis (6379)                    │
                                                                             │
   ┌─────────────────────────────────────────────────────────────────────────┘
   ▼ Railway internal network
   gepa (8080) — Python FastAPI sidecar for SKILL.md optimization
```

### Data flow at a glance

**Write path** — every event → Hooks Pipeline → SHA-256 dedup → `StripSecrets` → `mem_observations` → 384-dim embedding → FTS doc → `mem_audit` → (opt) Haiku compression → `mem_memories` + `mem_memory_sources`. In parallel: mirror to Honcho via `POST /v3/.../messages`.

**Read path** — query → BM25 + pgvector + graph traversal in parallel → RRF fusion → session diversification → fold in Honcho peer representation → injected as system-prompt prefix on the next turn.

**Acting path** — agent loop → `ToolGate.Authorize` → if blocked, insert Trust contract + synthesize `BLOCKED` tool result; if allowed, dispatch through `tools.Execute` (native, MCP, or Claude Code on Mac). Either way, hook fires → memory writes → next iteration.

---

## Phase status

| Phase | What | Status |
|---|---|---|
| 0 | Foundation | ✅ |
| 1 | Working text bot | ✅ |
| 2 | Tools + MCP + Settings MVP | ✅ |
| 3 | Memory: 12 tables, RRF, hooks, compression, provenance | ✅ |
| 4 | Skills: filesystem loader, sandbox tiers, agent tools, HTTP API | ✅ substrate · container sandbox WIP |
| 5 | Proactive: IntentFlow, WAL, Heartbeat, Trust queue, **curiosity gap-scan** | ✅ substrate · WS auto-fire WIP |
| 6 | Cron + Sentinels + Voyager + GEPA + source extractor + **Pareto frontier** + **autotrigger** | ✅ closed-loop self-evolution |
| 7 | Polish + Coding bridge + Honcho + custom domain | ✅ major wins shipped |
| **AGI** | **Reflection · Prediction · A-MEM · Sleep-time · Procedural tier · Curiosity** | ✅ migration 011 |
| 8 | Voice | — roadmap |

See [ARCHITECTURE.md](ARCHITECTURE.md) for the source-of-truth wiring diagram.

---

## Features

### AGI Loops (NEW — migration 011)
- **GEPA Pareto frontier** — `mem_skill_proposals` carries `frontier_run_id` + `score` + `pareto_rank`; `SampleFromFrontier` draws weighted from the frontier instead of locking to a single champion. ICLR 2026 Oral pattern.
- **Voyager autotrigger** — background ticker watches `mem_skill_runs` for skills past the failure threshold (30% default, 5-run window, 6h cooldown) and fires `RunOptimizer` automatically. Closes the failure → curriculum → skill → optimization cycle.
- **Procedural memory tier (CoALA)** — promoted skills materialize as `mem_memories` rows with `tier='procedural'`, embedded via the same embedder, retrieved through the same RRF pathway, injected into the system prompt before tool selection.
- **Reflection / metacognition** — `infinity reflect` walks recent sessions, runs a separate "critic persona" Haiku call (MAR pattern — actor doesn't get to grade itself), persists structured critique + lessons to `mem_reflections`. High-confidence lessons (`confidence ≥ 0.6`) auto-promote to `mem_lessons`. Importance inverts quality — bad sessions are *more* important to remember.
- **Predict-then-act** — `PreToolUse` records an expected outcome to `mem_predictions`; `PostToolUse` resolves with a Jaccard-based surprise score (0..1). Failure hooks force surprise ≥ 0.5. JEPA epistemic discipline without a generative world model.
- **A-MEM auto-linking** — every new episodic memory writes top-4 `'associative'` edges into `mem_relations` (cosine ≥ 0.65 floor). Retrieval can now traverse the graph, not just rank.
- **Sleep-time consolidation (8-op)** — `infinity consolidate` rebuilt as a distinct offline regime: decay → hot-reset → cluster → **contradiction resolution** (older memory of a `'contradicts'` pair → superseded) → **associative pruning** (keep top-10 outgoing per node) → **weak-edge purge** (drop confidence < 0.40) → **procedural re-weight** (strength = recent skill success rate) → forget.
- **Curiosity gap-scan** — heartbeat checklist now scans for low-confidence semantic memories, unresolved contradictions, uncovered graph mentions, and high-surprise predictions, then writes idempotent rows to `mem_curiosity_questions`. The proactive engine finds gaps, not just reacts to time.

### Memory & Recall
- **12-table memory store** — observations, summaries, semantic memories, sources, relations, profiles, graph nodes, graph edges, node-observation links, audit, lessons, sessions
- **Three new tables in migration 011** — `mem_reflections`, `mem_predictions`, `mem_curiosity_questions`
- **Triple-stream retrieval** — BM25 + pgvector HNSW + 2-hop graph BFS, fused with Reciprocal Rank Fusion (k=60)
- **Session diversification** — caps any single session at 3 hits per recall to prevent echo chambers
- **Provenance chain** — every memory links to its source observations via `mem_memory_sources`; `GET /api/memory/cite/:id` surfaces the full chain
- **Cascading staleness** — `MarkSuperseded` propagates through the memory graph
- **Haiku LLM compression** — strict-JSON entity extraction promotes raw observations into episodic memories, then auto-links to top-4 cosine-nearest neighbours via `'associative'` relations
- **Sleep-time consolidation** — `infinity consolidate` runs the 8-op offline regime (see AGI Loops above)
- **Privacy-first capture** — `memory.StripSecrets` runs 10 regex patterns + `<private>` tag stripping at the boundary
- **SHA-256 dedup** — 5-minute window prevents observation spam
- **Audit trail** — every memory operation writes a `mem_audit` row (table#id target)
- **FTS with synonyms** — `infinity_search` config with graceful fallback on managed Postgres
- **Honcho dialectic peer model** — runs alongside `mem_*` as a *who-is-the-boss* layer, chained via `agent.CompositeMemory`

### Coding (Phase 7 — NEW)
- **`claude_code` MCP server** — 25 tools surfaced over SSE through Cloudflare Tunnel to your home Mac's `claude mcp serve`
- **Max-subscription billing** — `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET` are the only credentials Railway holds; OAuth tokens never leave the Mac
- **`ClaudeCodeGate`** — defaults to gating `Bash`, `Write`, `Edit`; everything else passes; override via `INFINITY_CLAUDE_CODE_BLOCK` / `INFINITY_CLAUDE_CODE_AUTOAPPROVE`
- **Trust queue for coding** — every gated call lands in `mem_trust_contracts` with full action spec + tool input preview
- **iOS-friendly diff viewer** — `ToolCallCard` recognizes unified-diff output and renders per-line red/green
- **Code-mode composer toggle** — disables iOS autocorrect/capitalize/spellcheck when typing paths or commands
- **End-to-end captured into memory** — every coding session writes observations with provenance

### Agent Loop
- **Intentionally small loop** — nanobot-inspired, never imports memory/skills/hooks/honcho/proactive directly (interfaces only)
- **`ToolGate` interface** — pluggable authorization layer; `proactive.ClaudeCodeGate` is the production impl
- **`CompositeMemory`** — chains N memory providers; Searcher (RRF) + Honcho (dialectic) is the default
- **Streaming tokens** — delta / tool_call / tool_result / complete / error events over WebSocket
- **System-prompt prefix injection** — memory recall + Honcho peer rep + skill suggestions fold in before the first LLM call
- **Multi-iteration tool loop** — up to N tool round-trips per user turn
- **12 lifecycle hooks** — UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure, TaskCompleted, ToolGated, and 6 more

### Tools & Integrations
- **Native tools** — `http_fetch` (allowlisted domains), `web_search` (Tavily), `code_exec` (sidecar), `remember`, `recall`, `forget`
- **MCP client** — stdio + SSE transports, namespaced as `<server>__<tool>`, hot-loaded from `config/mcp.yaml`, supports `bearer` and `cloudflare_access` auth
- **Embedded MCP registry** — `mcp.yaml` `//go:embed`'d into the binary so distroless runtimes find it without source files
- **Pluggable LLM provider** — Anthropic, OpenAI, Google (stub) behind a single `Provider` interface
- **Pluggable embedder** — stub (deterministic) or HTTP sidecar (FastAPI), 384-dim vectors
- **Tool registry auto-exposes** — anything registered shows up in `/api/tools` and the agent's tool list

### Skills System (Phase 4) + Self-Evolution
- **Filesystem-native** — `SKILL.md` + YAML frontmatter, OpenClaw + Hermes-compatible (drop-in symlink)
- **Risk-tiered sandboxing** — process jail (low/medium) → container (high/critical, WIP)
- **Trigger matching** — Token Jaccard + substring overlap, threshold 0.5
- **Agent-callable** — `skills.list`, `skills.invoke`, `skills.discover`, `skills.history`
- **Run history** — every invocation persists to `mem_skill_runs` with success rate
- **Versioning** — `mem_skill_versions` + `mem_skill_active` for rollback
- **Hot reload** — `POST /api/skills/reload` re-walks the filesystem
- **GEPA optimizer + Pareto frontier (NEW)** — Genetic-Pareto loop that mutates SKILL.md from failure traces, hard-gated on size/frontmatter/non-noop, **persists the whole frontier** with `frontier_run_id` + `pareto_rank`, queued through Trust Contracts. `SampleFromFrontier` draws weighted by score for runtime A/B sampling.
- **Autotrigger (NEW)** — background ticker auto-fires GEPA on skills past the failure-rate threshold. The close-the-loop step. Off until `GEPA_URL` is set; tunable via `INFINITY_VOYAGER_FAILURE_RATE` / `INFINITY_VOYAGER_MIN_RUNS` / `INFINITY_VOYAGER_COOLDOWN`.
- **Procedural-memory promotion (NEW)** — every promoted skill writes a `tier='procedural'` row via `OnSkillPromoted` callback, so retrieval injects it through the same RRF pathway as semantic facts. CoALA's procedural tier, populated.
- **Voyager extractor** — at SessionEnd, drafts new SKILL.md candidates from observation transcripts
- **Voyager discovery** — counts repeated tool-triplets across sessions to spot crystallization opportunities
- **Voyager source extractor** — at SessionEnd, detects "file-fight" patterns (same file edited ≥3× with failures) and drafts a refactor proposal via Haiku into `mem_code_proposals`. Approval marks intent only — the actual edit still flows through `ClaudeCodeGate` → Trust queue, so self-modification of Go/Next.js source stays boss-gated.

### Proactive Engine (Phase 5)
- **IntentFlow detector** — Haiku classifies every turn into silent / fast / full + Quiet Hours gate
- **WAL extractor** — regex captures corrections, preferences, decisions, dates, URLs into `mem_session_state`
- **Working Buffer** — at 60% context utilization, snapshots into `mem_working_buffer` for recovery
- **Heartbeat ticker** — every 30 minutes runs the composed checklist (`DefaultChecklist` + `CuriosityChecklist`)
- **Curiosity gap-scan (NEW)** — composes into the heartbeat: scans low-confidence memories, unresolved contradictions, uncovered graph mentions, high-surprise predictions. Writes idempotent rows to `mem_curiosity_questions`. Surfaces as `Finding{Kind:"curiosity"}`.
- **Predict-then-act recorder (NEW)** — `hooks.PredictionRecorder` writes one row to `mem_predictions` per PreToolUse, resolves with surprise score on PostToolUse. Zero LLM cost — Jaccard heuristic.
- **Trust Contract queue** — anything risky lands in `mem_trust_contracts` for approval / denial / snooze; user_id-scoped
- **Outcome journal** — `mem_outcomes` tracks promised work and surfaces overdue items

### Cron & Sentinels (Phase 6)
- **Cron scheduler** — `mem_crons` rows + robfig/cron/v3 with UTC, standard 5-field parser
- **Two job kinds** — `system_event` (fixed session id) or `isolated_agent_turn` (fresh UUID per fire)
- **Failure tracking** — `failure_count` resets on success, transactional `last_run_*` updates
- **Schedule preview** — `POST /api/crons/preview` returns next-N fire times before saving
- **Sentinels** — `mem_sentinels` rows define watch_type + watch_config + action_chain + cooldown
- **Webhook trigger live** — `POST /api/sentinels/:id/trigger` enforces enabled + cooldown then dispatches
- **Skill dispatcher** — sentinels fire skill chains directly through the runner

### Studio (Next.js 14)
- **Eight tabs** — Live · Sessions · Memory · Skills · Heartbeat · Trust · Cron · Audit (+ Settings)
- **Live tab** — streaming chat; tool-call cards width-capped at 50%, thinking blocks at 50%, agent replies at 75% so the conversation has visual rhythm
- **Composer code-mode toggle** — turns off iOS autocorrect/spellcheck/capitalize and switches to monospace for typing paths/commands
- **Memory tab** — searchable list with tier badges, provenance chains, observation drill-down
- **Skills tab** — cards with last_run + success_rate, manual invoke, run history
- **Heartbeat tab** — interval config, recent runs, run-now button
- **Trust tab** — approval queue with approve / deny / snooze actions; deep-linked from gated tool cards via `?focus=<contract-id>`
- **Cron tab** — sub-tabs for crons + sentinels, schedule preview, enable toggles
- **Audit tab** — every memory operation, filterable by op
- **Settings tab** — provider config, model, env diagnostics
- **Unified-diff renderer** — tool cards detect unified-diff output and render with red/green per-line shading
- **Hydration discipline** — deferred UUIDs, `suppressHydrationWarning` on locale-dependent renders
- **shadcn/ui primitives** — Button, Card, Badge, Tabs, Dialog, ContextMenu, etc.
- **Lucide icons** throughout

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
- **Six-service Railway deploy** — `core`, `studio`, `gepa`, `honcho`, `honcho-deriver`, `redis`; pinned by `railway.toml`
- **Private custom domain via Cloudflare** — Studio served from your own subdomain (CNAME to studio's Railway URL); zero exposure of the raw `*.up.railway.app`
- **Cloudflare Tunnel + Access** — coder bridge re-uses an existing tunnel with a Service Token policy for Railway-only access
- **Embedded migrations** — `//go:embed db/migrations/*.sql`, no `db/` in the runtime container
- **Embedded `mcp.yaml`** — `core/config/embed.go` so distroless runtimes find the registry
- **Graceful degradation** — missing `DATABASE_URL` → memory off, missing LLM → server still serves health + memory, missing `HONCHO_BASE_URL` → peer rep skipped, missing `GEPA_URL` → optimize returns 503
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
# Sleep-time 8-op regime: decay, hot-reset, cluster, contradiction resolve,
# associative prune, weak-edge purge, procedural reweight, auto-forget.
go run ./cmd/infinity consolidate
go run ./cmd/infinity consolidate --compress # also runs Haiku observation→memory promotion + A-MEM auto-linking
go run ./cmd/infinity consolidate --dry-run  # preview deletions only
```

### 6b. Metacognition / reflection (NEW)

```sh
# Walk every session in the last 24h that doesn't yet have a reflection.
# Runs a fresh Haiku critic persona per session (~$0.01/session).
go run ./cmd/infinity reflect

# Override window or target a single session:
go run ./cmd/infinity reflect --window 168h --limit 50
go run ./cmd/infinity reflect --session <uuid>
go run ./cmd/infinity reflect --session <uuid> --force   # overwrite existing
```

### 7. Optional: home-Mac coding bridge

```sh
bash docs/claude-code/install.sh
# follow the runbook in docs/claude-code/SETUP.md
```

---

## Repository layout

```
infinity/
  core/                           # Go binary — self-contained, embedded migrations
    Dockerfile                    # build context = core/
    cmd/infinity/                 # cobra CLI: serve, migrate, doctor, consolidate, reflect
    config/                       # mcp.yaml + embed.go (//go:embed for distroless)
    db/migrations/                # 001_init → 011_agi_loops (embedded into binary)
    internal/
      agent/                      # loop.go + gate.go (ToolGate) + composite_memory.go
      llm/                        # Anthropic, OpenAI, Google + Haiku summarizer + critic
      tools/                      # Registry, MCP client (bearer/CF-Access), native tools
      memory/                     # store, search, RRF, compress (w/ A-MEM auto-link),
                                  #   forget, provenance, procedural, reflection,
                                  #   predictions, consolidate (sleep-time 8-op)
      hooks/                      # 12-event pipeline, capture, privacy, predict
      honcho/                     # client.go + MemoryProvider — dialectic peer modelling
      embed/                      # Embedder interface (stub | http sidecar)
      skills/                     # loader, sandbox, runner, registry tools, HTTP API
      intent/                     # IntentFlow detector + Quiet hours
      proactive/                  # WAL, working buffer, heartbeat, trust,
                                  #   gate (ClaudeCodeGate), curiosity gap-scan
      voyager/                    # extractor + verifier + discovery + optimizer (GEPA
                                  #   w/ Pareto frontier) + autotrigger
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
  docker/
    embed.Dockerfile              # optional FastAPI embed sidecar
    codeexec.Dockerfile           # Python code-exec sidecar
    gepa/                         # GEPA optimizer service (Dockerfile + server.py)
    honcho/                       # Honcho FastAPI build (clones upstream main)
    honcho-deriver/               # Honcho deriver worker build
  docs/
    claude-code/                  # Mac coder bridge runbook + launchd plists
    honcho/                       # Honcho deploy guide
    gepa/                         # GEPA optimizer guide
  railway.toml                    # pins rootDirectory per service
```

---

## Deploying to Railway

Six services on Railway, plus Postgres on Supabase.

1. **Postgres on Supabase** (recommended). Settings → Database → Connection string → use the **session pooler** URL (`aws-1-us-west-1.pooler.supabase.com:5432`) with `sslmode=require`. Free tier is IPv4-friendly. Set as `DATABASE_URL` on Core.

2. **Core service**. Source from this repo. Root Directory: `core`. Wire env: `DATABASE_URL`, `ANTHROPIC_API_KEY`, optional `CLAUDE_CODE_TUNNEL_URL` + `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET` for the coding bridge, optional `HONCHO_BASE_URL`, optional `GEPA_URL`.

3. **Studio service**. Same repo. Root Directory: `studio`. Wire env: `NEXT_PUBLIC_CORE_URL=https://core.up.railway.app`, `NEXT_PUBLIC_CORE_WS_URL=wss://core.up.railway.app/ws`. For a custom domain, `railway domain --service studio <subdomain.example.com>` and add the returned CNAME to your DNS.

4. **GEPA service** (optional). Root Directory: `docker/gepa`. Set `ANTHROPIC_API_KEY=${{core.ANTHROPIC_API_KEY}}`. Set `GEPA_URL=http://gepa.railway.internal:8080` on core.

5. **Honcho services** (optional). Two services: `honcho` (root `docker/honcho/`) and `honcho-deriver` (root `docker/honcho-deriver/`). Plus a Redis service (`redis:7-alpine`). All four use Railway reference variables to share `${{core.DATABASE_URL}}` and `${{core.ANTHROPIC_API_KEY}}`.

6. **Migrate once**: `cd core && DATABASE_URL=… go run ./cmd/infinity migrate`. Or set `infinity migrate && infinity serve` as the Core start command.

`railway.toml` pins `rootDirectory` per service so auto-detection lines up.

---

## License

Private. Lifts and ports from `rohitg00/agentmemory` (Apache-2.0) per the build plan. Honcho integration consumes [plastic-labs/honcho](https://github.com/plastic-labs/honcho) (AGPL-3.0) as a separate service over HTTP — no source copied into this repo.
