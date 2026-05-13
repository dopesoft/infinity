# Infinity

**Your always-on AI that actually learns.**

Not a chatbot. Not a wrapper. A permanent cognitive substrate that remembers everything, codes on your home Mac under your Max subscription, reflects on its own work between conversations, predicts before it acts and notices when it's wrong, rewrites its own failing skills, and asks you the questions you didn't ask. **The version of it next week is genuinely smarter at your problems than the version of it today.**

> Hermes lights up on demand. Nanobot pipes tools around. OpenClaw runs skills on every channel.
> Infinity does all of it — plus the one thing none of them do: it gets sharper between conversations, on its own, while you sleep.

---

## What's new

**Infinity actually *learns*.** Hermes stores messages. OpenClaw runs scripts. Nanobot pipes tools around. Infinity does all that *plus* sharpens itself between every conversation — so the version of it you're talking to next week is genuinely smarter at your problems than the one you're talking to today. Eight new muscles:

- **🧠 Reflection (MAR-style critic loop)** — every session gets replayed by a separate critic mind that scores how it went and writes down lessons. **The next time you ask the same kind of thing, Infinity hits it cleaner — without you having to coach it twice.**
- **🔮 Predict-then-act (JEPA discipline)** — before every tool call, Infinity writes down what it expects to happen. After, it scores how surprised it was. **When reality keeps disagreeing with the agent's gut, Infinity catches it and pivots — instead of confidently doing the wrong thing five times in a row.**
- **🎯 Procedural memory tier (CoALA)** — skills the agent builds from watching you become first-class instincts. **You stop reminding it which tool to reach for. The longer you use it, the more it just *knows* your workflow.**
- **🕸️ A-MEM auto-linking** — every new memory gets stitched into the four most-related things Infinity already knows. **Ask about one thread and you get the whole rope — context, history, related decisions, all at once.**
- **💤 Sleep-time consolidation (LightMem)** — every night the agent decays stale memories, resolves contradictions in its own head, prunes weak connections, and re-weights skills by how well they're working. **You wake up to a sharper agent, not a noisier one. Most agents accumulate junk forever; Infinity self-grooms.**
- **❓ Curiosity gap-scan** — Infinity scans its own knowledge for things it's losing confidence in, names you've mentioned but never explained, predictions that didn't pan out. **It asks you the question, instead of guessing and getting it wrong.**
- **🧬 GEPA Pareto frontier (ICLR 2026 Oral)** — every time a skill fails, Infinity spawns a family of variants, races them, and keeps the whole leaderboard — not just the top one. **You can crown any of them king, or let the agent A/B silently on calls where it's unsure.**
- **🚨 Voyager autotrigger** — when a skill's failure rate creeps up, Infinity quietly fires the rewriter on its own. **A fresh, better version is waiting in your Trust queue before you'd have noticed the old one was struggling.**

Plus everything that came before:

- **🛠️ Codes on your home Mac under your Max subscription.** 25 tools (Bash, Edit, Write, Read, Grep, Glob, Agent, WebFetch, …) wired through a private tunnel. No API tokens billed for code work. Every dangerous call asks first — one-tap approve from your phone.
- **🧠 Honcho-backed model of *you*.** A continuously-updating peer rep of your projects, habits, in-flight thinking — folded into every reply. Privacy-filtered before anything ever lands.
- **🌐 Lives on your domain.** Open it on your phone at your address. Not a Railway URL, not someone's app.

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
| **Connects every new memory to related ones automatically** | ✅ traverses the web, not just ranks by score | ❌ | ❌ | ❌ |
| **Habits — skills become first-class instincts** | ✅ surfaced before the agent shops for tools | ⚠️ skill registry only | ⚠️ skill files | ❌ |
| **Reflects on its own work + saves the lessons** | ✅ separate critic mind, per-session | ❌ | ❌ | ❌ |
| **Predicts outcomes, scores its own surprise** | ✅ feeds curiosity + curriculum | ❌ | ❌ | ❌ |
| **Nightly self-maintenance** (cleanup, contradiction-resolution, skill re-weighting) | ✅ | ❌ | ❌ | ❌ |
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
| **Keeps the whole family of skill variants** (not just one winner) | ✅ promote any of them, A/B silently | ❌ single champion | ❌ | ❌ |
| **Rewrites failing skills on its own** | ✅ proposal waiting before you'd have noticed | ❌ manual | ❌ | ❌ |
| **Self-noticing** — refactor proposals from file-fight patterns | ✅ Voyager source extractor → `mem_code_proposals` | ❌ | ❌ | ❌ |
| Skill versions + rollback | ✅ `mem_skill_versions` + `mem_skill_active` | ❌ | ⚠️ | ❌ |
| Run history with success rate | ✅ `mem_skill_runs` | ⚠️ | ⚠️ | ❌ |
| Trigger-phrase matching at turn-start | ✅ Token Jaccard, threshold 0.5 | ⚠️ | ✅ | ❌ |
| Public skill registry | ❌ (private skills/) | ✅ agentskills.io | ✅ ClawHub | ❌ |

### Autonomy (acts without being asked)

| Capability | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Proactive heartbeat (agent ticks on its own) | ✅ 30-min checklist | ❌ | ⚠️ via cron | ❌ |
| **Asks you the questions you didn't ask** | ✅ surfaces gaps, contradictions, surprises | ❌ | ❌ | ❌ |
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
| **Infinity** | **The only agent that actually *learns* between conversations** — reflects on its own work, expects vs. surprised, asks you about its own gaps, rewrites failing skills on its own, codes on your Max subscription, runs proactively on its own clock, and lives at your URL on your phone. The version of it next week is genuinely smarter than the version of it today. |
| Hermes | Closest peer. Same skill-file lineage, broader channel reach. **Stores messages but doesn't reflect on them, doesn't predict, doesn't ask its own questions, doesn't run a Pareto frontier — picks one winner and moves on.** |
| OpenClaw | The parent project of both Infinity's and Hermes's skill format. Strongest at channel reach (22+) and voice. **A great assistant; not a learning one.** |
| Nanobot | A pure MCP host. Plug servers in, chat with them. **No memory, no skills, no autonomy. A different category, not a competitor.** |

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
| 6 | Cron + Sentinels + Voyager + GEPA + Pareto frontier + autotrigger | ✅ closed-loop self-evolution |
| 7 | Polish + Coding bridge + Honcho + custom domain | ✅ major wins shipped |
| **AGI** | **Reflection · Prediction · A-MEM · Sleep-time · Procedural tier · Curiosity** | ✅ shipped |
| 8 | Voice | — roadmap |

See [ARCHITECTURE.md](ARCHITECTURE.md) for the source-of-truth wiring diagram.

---

## Features

### AGI Loops (the closed-loop layer no competitor has)

- **Reflection (MAR critic persona)** — fire `infinity reflect` and the agent replays every recent session with a separate critic mind, scores how it went, writes structured lessons it can pull from later. **You don't have to repeat yourself. The agent corrects its own habits between sessions, not when you catch it doing the thing again.**
- **Predict-then-act (JEPA-style discipline)** — before each tool call the agent records what it expects; after, it scores its own surprise. Failures and wild misses bubble straight into the curriculum. **The agent stops being cocky about things it keeps getting wrong, and starts asking before it digs the hole deeper.**
- **Procedural memory tier (CoALA)** — skills the agent crystallises from your work get embedded and injected into its system prompt every turn. **It reaches for habits first, tools second. The longer you use it, the less it has to think about *how* — and the more it can think about the actual problem.**
- **A-MEM auto-linking** — every new memory wires itself to its closest existing neighbours at write time, not at retrieval time. **Pulling one fact pulls the cluster. Your context shows up complete — the decision, the rationale, the contradicting fact, the related project — instead of just the keyword match.**
- **Sleep-time consolidation (LightMem-style)** — every night the agent decays old memories, resolves contradictions in its own head, prunes weak connections, and re-weights skills by how well they've been working. **You wake up to a sharper agent. Everyone else's agent accumulates lint until performance tanks; Infinity self-grooms.**
- **Curiosity gap-scan** — the heartbeat scans for things the agent is losing confidence in, names you've mentioned but never explained, predictions that didn't pan out. **The agent asks you the question instead of bluffing the answer. It admits what it doesn't know — and fixes it on purpose.**
- **GEPA Pareto frontier (ICLR 2026 Oral)** — when a skill fails, the optimizer spawns a family of rewrites, races them, and saves the whole leaderboard, not just the top one. **You promote any version you like. The agent can run a non-champion variant when it's unsure, then learn from how it went. 35× more efficient than RL on the same workload.**
- **Voyager autotrigger** — a background watcher fires the optimizer the moment a skill's failure rate creeps past your threshold. **A fresh proposal is in your Trust queue before you'd have manually noticed the skill was struggling. The improvement loop runs without you babysitting it.**

### Memory & Recall
- **Full relational + vector + graph store** — 15+ purpose-built tables under the hood, including a reflection tier, a prediction log, and a curiosity backlog. **Every observation, every memory, every connection, every lesson, every surprise — all recorded, all searchable.**
- **Triple-stream retrieval (RRF)** — keyword + vector + graph traversal in parallel, fused. **The agent finds the right memory even when you don't use the words you used last time.**
- **Session diversification** — one chatty session never dominates recall. **No echo-chamber from a single afternoon.**
- **Provenance chain** — every memory traces back to the exact observations that produced it. **Ask the agent *why does it think X* and it shows you the trail.**
- **Cascading staleness** — corrections propagate. **Tell it once that something changed and every downstream memory updates with it.**
- **Haiku-driven compression + A-MEM auto-linking** — raw chatter gets distilled into structured memories *and* wired into the existing web in a single pass.
- **Sleep-time consolidation** — see AGI Loops above.
- **Privacy filter at the capture boundary** — secret scrub + `<private>` tag stripping before anything hits the database. **API keys, tokens, passwords never get stored, even if you paste them at it.**
- **5-minute dedup** — accidental re-runs don't pollute the store.
- **Full audit log** — every memory operation recorded and queryable. **Nothing happens in the dark.**
- **FTS with synonyms** — search "db" and it finds "database."
- **Honcho dialectic peer model** — *who you are* runs as a continuously-updating layer alongside *what you said*.

### Coding (Claude Code over MCP)
- **25 tools wired to your home Mac** — Bash, Edit, Write, Read, Grep, Glob, Agent, WebFetch, and more, all over a private Cloudflare tunnel.
- **Runs on your Max subscription, not API tokens.** ToS-clean — your OAuth credentials never leave your Mac. **You're already paying for the Max plan; now your always-on agent uses it too.**
- **Trust-gated by default** — anything that writes or executes asks first. Reads pass through. **You sleep without worrying the agent went rogue at 3am.**
- **Approve from your phone** — every dangerous call lands in your Trust queue with a diff preview and a one-tap approve button.
- **iOS-friendly diff viewer** — unified diffs render with per-line red/green right in chat.
- **Code-mode composer toggle** — disables iOS autocorrect when you're typing paths or commands. **No more autocapitalised filenames.**
- **Every coding session goes into memory with full provenance** — the agent remembers the change *and* why it made it.

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

### Skills + Self-Evolution
- **Drop-in compatible with OpenClaw / Hermes skills** — write a `SKILL.md` file, the agent loads it. Bring your existing skill library; nothing to convert.
- **Risk-tiered sandboxing** — low/medium-risk skills run in a process jail; high/critical land in containers (in progress). **You don't have to read every skill before you let it run.**
- **Trigger matching** — the agent suggests the right skill when the conversation matches what it knows how to do.
- **Run history with success rates** — every skill execution is logged. **You see at a glance which ones are actually working.**
- **Versioned + rollback-able** — every skill change keeps a history. **Revert in one tap if a promotion regresses.**
- **GEPA optimizer with Pareto frontier (ICLR 2026 Oral)** — when a skill fails, a family of rewrites compete; the whole leaderboard lands in your Trust queue. **Promote any rank you want, or let the agent A/B sample silently.**
- **Voyager autotrigger** — a background watcher fires the optimizer the moment failure rate creeps up. **The improvement cycle runs on its own.**
- **Procedural-memory promotion (CoALA)** — every approved skill becomes a habit the agent reaches for automatically. **It stops asking *which tool* and starts asking *what next*.**
- **Voyager extractor** — at the end of every session, Infinity notices "you just taught me a thing" and drafts a new skill for your review. **The agent learns from watching you, not from waiting for you to write a SKILL.md.**
- **Voyager discovery** — repeated tool sequences across sessions become candidate skills. **The agent crystallises your habits before you've even named them.**
- **Voyager source extractor** — when you fight the same file three times in a session, Infinity drafts a refactor proposal. **Autonomous *noticing*, never autonomous writing — the actual edit still asks for approval.**

### Proactive Engine
- **IntentFlow** — every turn is classified into silent / fast / full assistance. **The agent matches its energy to yours instead of treating every question like a research project.**
- **Quiet Hours** — set your hours, the agent shuts up. **No notifications at 2am.**
- **Working memory** — when context starts filling, the agent snapshots the load-bearing fragments so a fresh session can pick up exactly where you left off.
- **Heartbeat** — every 30 minutes the agent runs its own checklist: overdue commitments, repeated patterns, struggling skills, **gaps in its own knowledge, predictions that didn't pan out, contradictions in what it knows.**
- **Curiosity gap-scan** — see AGI Loops above. **The agent asks you about its blind spots, on its own, between conversations.**
- **Predict-then-act recorder** — see AGI Loops above. **Every tool call comes with a measured surprise score.**
- **Trust Contract queue** — every risky action waits for your nod. Approve, deny, or snooze. Approve from your phone.
- **Outcome journal** — promises and commitments get tracked. **"You said you'd review this on Friday" — the agent remembers, even if you don't.**

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
