# Infinity

**Your always-on AI that actually learns.**

Other agents store messages. Infinity reviews its own work between conversations, catches its mistakes, builds new habits, asks you the questions you didn't ask, and quietly rewrites the parts of itself that aren't working. The version of it next week is genuinely sharper at your problems than the version of it today.

It also codes on your home Mac under your Max subscription, lives at your own URL on your phone, and queues every dangerous action for one-tap approval.

---

## The honest comparison

| | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| **How it remembers** | | | | |
| Persistent memory across sessions | ✅ relational + vector + graph | ✅ FTS5 + peer rep | ⚠️ flat MEMORY.md files | ❌ |
| Provenance — every memory cites its source | ✅ | ❌ | ❌ | ❌ |
| Auto-links new memories to related ones at write time | ✅ | ❌ | ❌ | ❌ |
| Resolves contradictions in its own head, nightly | ✅ | ❌ | ❌ | ❌ |
| Continuously-updated model of *who you are* | ✅ Honcho | ✅ Honcho | ❌ | ❌ |
| Privacy filter strips secrets before storage | ✅ | ❌ | ❌ | ❌ |
| Full audit log of every memory write | ✅ | ❌ | ❌ | ❌ |
| **How it learns** | | | | |
| Reflects on its own past sessions + saves lessons | ✅ | ❌ | ❌ | ❌ |
| Predicts each tool outcome, scores its own surprise | ✅ | ❌ | ❌ | ❌ |
| Crystallises new skills from watching you work | ✅ | ❌ | ❌ | ❌ |
| Detects when you keep fighting the same file → drafts a refactor | ✅ | ❌ | ❌ | ❌ |
| Asks you about its blind spots, on its own | ✅ | ❌ | ❌ | ❌ |
| Rewrites failing skills automatically | ✅ | ❌ | ❌ | ❌ |
| Keeps the whole family of skill variants (not just one) | ✅ | ❌ single winner | ❌ | ❌ |
| **What it can do** | | | | |
| Code on local files (edit, run, refactor) | ✅ 25 tools | ✅ | ✅ | ⚠️ via MCP |
| Use your Max subscription instead of API tokens | ✅ uniquely ToS-clean | ❌ API key | ❌ API key | ❌ API key |
| Multi-file refactors with sub-agents | ✅ | ✅ | ⚠️ | ⚠️ |
| Per-call approval gate with phone deep-link | ✅ | ❌ | ⚠️ allow/deny lists | ❌ |
| Heartbeat — runs on its own schedule | ✅ | ❌ | ⚠️ via cron | ❌ |
| Cron-scheduled agent runs | ✅ | ❌ | ✅ | ❌ |
| Webhook-triggered actions | ✅ | ❌ | ⚠️ | ❌ |
| **How you reach it** | | | | |
| Open it on your phone | ✅ mobile-first PWA | ❌ | ⚠️ | ⚠️ |
| Custom domain at your address | ✅ | ❌ | ❌ | ❌ |
| WhatsApp / Slack / Telegram / iMessage | ❌ | ✅ 8+ channels | ✅ 22+ channels | ⚠️ MCP-UI |
| Voice (wake word, dictation) | ❌ on roadmap | ❌ | ✅ | ❌ |
| Public skill marketplace | ❌ | ✅ agentskills.io | ✅ ClawHub | ❌ |
| Open source | ⚠️ private repo | ✅ MIT | ✅ | ✅ |
| Hundreds of LLMs via OpenRouter | ❌ Anthropic primary | ✅ 200+ | ✅ multi | ✅ multi |

**Where Infinity wins:** memory, learning loops, mobile, custom domain, ToS-clean coding on your existing subscription.
**Where Hermes wins today:** open source, multi-LLM reach, skill marketplace, more deployment backends.
**Where OpenClaw wins today:** voice, channel reach (WhatsApp/Telegram/etc.), public hub.
**Where Nanobot wins today:** pure MCP host — best if all you want is plugging servers into a chat UI.

If you've already written skills for OpenClaw or Hermes, **drop them into `./skills/` and Infinity loads them unmodified.**

---

## What it can do for you

### Remembers everything that matters

Nothing said to Infinity disappears. Every conversation, every tool call, every decision is captured, structured, and searchable. When something contradicts what it knew before, the older fact is marked stale and the trail updates. When you correct it, every downstream memory updates with the correction.

Ask it *why does it think X* and it shows you the chain — the original observations, the decisions, the related projects, the dates. It can't bluff because it always carries the receipt.

**The tactics behind it:** parallel keyword + vector + graph retrieval (RRF k=60), session diversification, automatic associative linking at write time (A-MEM), cascading staleness through a typed memory graph, full audit log.

### Learns between conversations

After every session, a separate critic mind replays what happened, scores how it went, and writes down lessons. Those lessons feed forward. You don't have to coach Infinity twice.

Before each tool call, it writes down what it expects. After, it scores its own surprise. When reality keeps disagreeing with its gut, that becomes the next thing it studies — instead of confidently digging the same hole five times.

Every night, while you sleep, it cleans house: decays stale memories, prunes weak connections, resolves contradictions in its own head, and re-weights its own skills by how well they've been working. You wake up to a sharper agent, not a noisier one.

**The tactics behind it:** Park-style reflection trees with a MAR-pattern critic persona, JEPA-style predict-then-act surprise scoring, LightMem-style sleep-time consolidation. None of the competitors do any of this.

### Builds habits from watching you

Every session, Infinity notices the procedures you keep running and crystallises them into skills you can promote, edit, or reject. Repeated tool sequences across multiple sessions become candidate habits before you've even named them.

When you fight the same file three times in a session — multiple edits, failing builds — it drafts a refactor proposal so you can decide what to do about it. (It never makes the edit on its own; that still flows through your approval queue.)

Once a skill is approved, it becomes a first-class instinct: the agent reaches for it automatically before it goes hunting through tools.

**The tactics behind it:** Voyager-style session extraction, real-time tool-triplet discovery, source-fight detection, procedural-tier memory promotion (CoALA).

### Rewrites its own failing skills

When a skill's success rate creeps down, Infinity quietly fires the rewriter on its own. A family of rewrites compete, the leaderboard lands in your Trust queue, and you can promote any version you like. The improvement loop runs without you babysitting it — by the time you'd have noticed something was off, a fresh proposal is waiting.

**The tactics behind it:** GEPA optimizer (ICLR 2026 Oral) with full Pareto frontier persistence, background autotrigger watching skill-run failure rates, hard size/format gates on every candidate.

### Asks you the questions you didn't ask

Infinity scans its own knowledge for blind spots — facts it's losing confidence in, names you've mentioned but never explained, predictions that didn't pan out, two of its own memories that contradict each other. It surfaces them as questions for you instead of bluffing the answer.

It's curious *about you*, on purpose, between conversations.

### Codes on your hardware, under your subscription

25 tools — Bash, Edit, Write, Read, Grep, Glob, Agent, WebFetch, and more — wired from the agent over a private tunnel to your home Mac. Reads pass through immediately. Anything that writes or executes queues for your approval with a diff preview, and you can tap-approve from your phone.

Crucially, it runs under your Max subscription, not API tokens. You're already paying for the plan — your always-on agent gets to use it too. Your OAuth credentials never leave your Mac.

**The tactics behind it:** MCP-over-SSE through Cloudflare Tunnel + Access service tokens, `ClaudeCodeGate` routing risky verbs to a Trust queue, unified-diff renderer in Studio, code-mode composer toggle on iOS.

### Acts on its own schedule

Infinity runs a 30-minute heartbeat on its own, checking for overdue commitments, repeating patterns it could automate, struggling skills, and the curiosity gaps mentioned above. It tracks promises ("you said you'd review this on Friday") and surfaces them when due. It classifies every turn for energy level — silent, fast intervention, full assistance — so it matches your pace instead of researching every casual question.

You can also schedule arbitrary agent runs on cron and wire webhooks to fire skill chains. All risky actions still wait for approval.

**The tactics behind it:** IntentFlow detector (Haiku), Quiet Hours, working-memory snapshots at 60% context, Heartbeat ticker, Trust-Contract queue, robfig cron + sentinel manager.

### Lives where you live

Infinity is a mobile-first PWA at your own domain. The interface is hardened for iOS Safari — sticky composer above the keyboard, safe-area aware, 44px touch targets, WebSocket auto-reconnect when you bring it back from the background. Eight tabs (Live · Sessions · Memory · Skills · Heartbeat · Trust · Cron · Audit) and a code-mode composer that turns off autocorrect when you're typing paths.

Open it on your phone at your URL. Approve coding actions one-handed.

---

## Architecture map

```
                 ┌──────────────────────┐
   iPhone ─────► │  Studio (Next.js 14) │   <boss's private domain>
                 └──────────┬───────────┘   (CNAME via your DNS)
                            │ HTTPS + WSS
                            ▼
                 ┌──────────────────────────────────────────────────┐
                 │  Core (Go 1.26)                                  │
                 │  agent loop · tool registry · memory · hooks     │
                 │  Trust gate · proactive · cron · sentinels       │
                 │  Voyager (extractor + autotrigger) · GEPA client │
                 └──────────────────┬───────────────────────────────┘
                                    │ pgxpool
                                    ▼
                          Postgres + pgvector
                          (Supabase session pooler)
                                    ▲
                                    │ MCP/SSE + CF Access
                                    │
                          Home Mac · cloudflared · mcp-proxy · claude mcp serve
                                                                     ▲
                          ┌──────────────────────────────────────────┘
                          ▼ Railway private network
                          honcho + honcho-deriver + redis + gepa
```

**Write path:** every event → privacy filter → observation table → embedded + indexed → audit log → optionally promoted to memory + auto-linked to top-4 neighbours. In parallel: mirrored to the peer-modelling layer.

**Read path:** keyword + vector + graph retrieval in parallel → RRF fusion → session diversification → fold in peer rep → injected as system-prompt prefix.

**Acting path:** tool call → authorization gate → if blocked, queue a Trust contract and tell the model; if allowed, dispatch. Hooks fire on both sides → memory writes → next iteration. Pre- and post-call predictions get paired and scored automatically.

Full wiring, schema, HTTP API, and source-of-truth boot sequence in [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Phase status

| Phase | What | Status |
|---|---|---|
| 0 | Foundation | ✅ |
| 1 | Working text bot | ✅ |
| 2 | Tools + MCP + Settings MVP | ✅ |
| 3 | Memory: relational + vector + graph, RRF retrieval, hooks, compression, provenance | ✅ |
| 4 | Skills: filesystem loader, sandbox tiers, agent tools, HTTP API | ✅ substrate · container sandbox WIP |
| 5 | Proactive: intent detection, working buffer, heartbeat, trust queue, curiosity scan | ✅ |
| 6 | Cron + Sentinels + Voyager + GEPA + Pareto frontier + autotrigger | ✅ closed-loop self-evolution |
| 7 | Polish + coding bridge + peer modelling + custom domain | ✅ |
| **Learning** | Reflection · prediction · associative links · sleep consolidation · procedural tier · curiosity | ✅ |
| 8 | Voice | — roadmap |

See [ARCHITECTURE.md](ARCHITECTURE.md) for the per-phase gap list.

---

## Run it yourself

You can have Infinity running locally in about ten minutes. Need: Go 1.22+, pnpm 11+, Docker.

**1. Stand up the database.**

```sh
docker run -d --name infinity-pg \
  -e POSTGRES_USER=infinity -e POSTGRES_PASSWORD=infinity -e POSTGRES_DB=infinity \
  -p 5432:5432 pgvector/pgvector:pg16
```

**2. Drop in your keys.**

```sh
cp .env.example .env
# minimum: ANTHROPIC_API_KEY
```

**3. Apply the schema, boot the agent.**

```sh
cd core
DATABASE_URL=postgres://infinity:infinity@localhost:5432/infinity?sslmode=disable \
  go run ./cmd/infinity migrate
go run ./cmd/infinity serve --addr :8080
```

**4. Boot the phone-friendly UI.**

```sh
cd studio
pnpm install && pnpm dev
# open http://localhost:3000
```

That's it. Talk to it. Watch the Memory tab fill up.

### Wake the learning loops

Two background jobs that make Infinity get sharper. Run them on a cron, or fire them manually whenever you want to compound:

```sh
# Replay yesterday's sessions, score them, save the lessons. ~$0.01/session.
go run ./cmd/infinity reflect

# Nightly cleanup — decay, prune, resolve contradictions, re-weight skills.
go run ./cmd/infinity consolidate
```

### Plug in your Mac for coding

```sh
bash docs/claude-code/install.sh   # runbook in docs/claude-code/SETUP.md
```

Wires your home Mac into Infinity as the coding hands — runs under your Max subscription, never leaks OAuth tokens off the machine.

### Other useful CLI

```sh
go run ./cmd/infinity doctor       # env + DB + extensions + sidecars
go run ./cmd/infinity migrate      # apply embedded schema
```

---

## How the codebase is laid out

Just enough to know where to start reading. Full source-of-truth wiring in [ARCHITECTURE.md](ARCHITECTURE.md).

```
infinity/
  core/        Go binary — agent loop, memory, hooks, all server logic.
               Self-contained. Embedded schema. One binary, one container.
  studio/      Next.js 14 PWA — eight tabs, mobile-first, iOS-hardened.
  docker/      Optional sidecars: code execution, embedder, peer-modelling
               services, skill optimizer.
  docs/        Runbooks: home-Mac coding bridge, peer modelling, optimizer.
  railway.toml Pins each service to its build context.
```

---

## Putting it in production

Six services on Railway plus Postgres on Supabase. Only Core and Studio are public; everything else lives on Railway's private network.

| Service | Required? | What it does |
|---|:-:|---|
| **Core** | ✅ | The agent — memory, hooks, tools, Trust queue, learning loops |
| **Studio** | ✅ | The phone-friendly UI; bring your own domain for the "lives at your URL" bit |
| **GEPA** | optional | Skill self-rewriter — turn it on and the agent fixes its own failing skills |
| **Honcho + deriver** | optional | Continuously-updated peer model of *you* |
| **Redis** | with Honcho | Honcho's cache + queue |

Wire `DATABASE_URL` and `ANTHROPIC_API_KEY` on Core. Add the optional sidecars when you want their loops to activate — Infinity degrades gracefully without them. Studio takes `NEXT_PUBLIC_CORE_URL` + `_WS_URL` so it can talk to Core; `railway domain` gets you the custom subdomain.

To migrate once: `cd core && go run ./cmd/infinity migrate` against `DATABASE_URL`, or chain it into Core's start command.

Full env-var reference, service-by-service deploy notes, Supabase pooler details, and the home-Mac tunnel setup in [ARCHITECTURE.md § Deployment](ARCHITECTURE.md#15-deployment).

---

## License

Private. Lifts and ports from `rohitg00/agentmemory` (Apache-2.0) per the build plan. Peer modelling consumes [plastic-labs/honcho](https://github.com/plastic-labs/honcho) (AGPL-3.0) as a separate service over HTTP — no source copied into this repo.
