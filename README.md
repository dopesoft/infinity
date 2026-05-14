# Infinity

**Your always-on AI that actually learns.**

Other agents store messages. Infinity reviews its own work between conversations, catches its mistakes, builds new habits, asks you the questions you didn't ask, assembles whole workflows from a plain-English request, and quietly rewrites the parts of itself that aren't working. The version of it next week is genuinely sharper at your problems than the version of it today.

It also codes on your home Mac under your Max subscription, lives at your own URL on your phone, and queues every dangerous action for one-tap approval.

---

## The honest comparison

Verified May 2026 against each project's public repo + docs — `NousResearch/hermes-agent`, `openclaw/openclaw`, `nanobot-ai/nanobot`. Where a competitor's capability is plugin- or backend-dependent rather than native, it's marked ⚠️.

| | **Infinity** | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| **How it remembers** | | | | |
| Persistent memory across sessions | ✅ relational + vector + graph | ✅ FTS5 + markdown + Honcho | ✅ markdown + SQLite + vector | ❌ |
| Provenance — every memory cites its source | ✅ | ❌ | ⚠️ via Memory Wiki plugin | ❌ |
| Auto-links new memories to related ones at write time | ✅ | ❌ | ⚠️ via plugin | ❌ |
| Resolves contradictions in its own head | ✅ nightly | ❌ | ⚠️ "Dreaming" + plugin | ❌ |
| Continuously-updated model of *who you are* | ✅ Honcho | ✅ Honcho | ⚠️ Honcho (opt-in backend) | ❌ |
| Privacy filter strips secrets before storage | ✅ | ❌ | ❌ | ❌ |
| Full audit log of every memory write | ✅ | ⚠️ generic event hooks | ❌ | ❌ |
| **How it learns** | | | | |
| Reflects on its own past sessions + saves lessons | ✅ critic persona | ⚠️ FTS5 summarization | ✅ "Dreaming" consolidation | ❌ |
| Predicts each tool outcome, scores its own surprise | ✅ | ❌ | ❌ | ❌ |
| Crystallises new skills from watching you work | ✅ | ✅ after complex tasks | ⚠️ via community skill | ❌ |
| Detects when you keep fighting the same file → drafts a refactor | ✅ | ❌ | ❌ | ❌ |
| Asks you about its blind spots, on its own | ✅ | ⚠️ periodic nudges | ⚠️ synthesis sessions | ❌ |
| Rewrites failing skills automatically | ✅ GEPA autotrigger | ⚠️ manual skill edit | ❌ | ❌ |
| Keeps the whole family of skill variants (not just one) | ✅ | ❌ single version | ❌ | ❌ |
| **What it can do** | | | | |
| Code on local files (edit, run, refactor) | ✅ 25 tools | ✅ 40+ tools | ✅ ~7 core tools | ⚠️ only via attached MCP |
| Coding under your Claude/ChatGPT subscription, not API tokens | ✅ Claude Max | ❌ API key | ✅ ChatGPT/Codex OAuth | ❌ API key |
| Multi-file refactors with sub-agents | ✅ | ✅ | ⚠️ | ⚠️ |
| Per-call approval gate | ✅ + phone deep-link | ✅ shell hooks | ⚠️ sender-pairing gate | ❌ |
| Heartbeat — runs on its own schedule | ✅ | ⚠️ periodic nudges | ✅ | ❌ |
| Cron-scheduled agent runs | ✅ | ✅ | ✅ | ❌ |
| Webhook-triggered actions | ✅ | ✅ | ✅ | ❌ |
| **What it assembles** | | | | |
| Builds multi-step workflows from a sentence — durable, resumable | ✅ | ⚠️ script pipelines (not durable) | ⚠️ TaskFlow | ❌ |
| Writes *and activates* its own skills at runtime | ✅ live | ✅ `skill_manage` | ⚠️ installs from hub | ❌ |
| Wires new APIs / MCP servers into itself at runtime | ✅ agent-driven | ⚠️ config-level MCP | ⚠️ config-level MCP | ⚠️ config-level MCP |
| Scorecards — tracks whether its own work is actually working | ✅ | ❌ | ❌ | ❌ |
| Holds *its own* goals and pursues them autonomously | ✅ | ❌ | ⚠️ heartbeat, no goal store | ❌ |
| Decides when to interrupt you vs. batch it into a digest | ✅ | ❌ | ⚠️ | ❌ |
| **How you reach it** | | | | |
| Open it on your phone | ✅ mobile-first PWA | ✅ PWA workspace | ✅ iOS + Android nodes | ❌ localhost web UI |
| Custom domain at your address | ✅ | ❌ | ⚠️ Tailscale / remote | ❌ |
| WhatsApp / Slack / Telegram / iMessage | ❌ | ⚠️ 20+ channels, no iMessage | ✅ 22+ channels, incl. iMessage | ❌ |
| Voice | ✅ GPT Realtime | ✅ realtime | ✅ wake word + talk mode | ❌ |
| Public skill marketplace / hub | ❌ | ✅ Skills Hub | ✅ ClawHub | ❌ |
| Open source | ⚠️ private repo | ✅ MIT | ✅ MIT | ✅ Apache-2.0 |
| Many LLMs via OpenRouter | ❌ Anthropic primary | ✅ OpenRouter 200+ | ⚠️ multi-LLM, no OpenRouter | ⚠️ multi-LLM, no OpenRouter |

**Where Infinity wins — the half that compounds:** the *depth* of memory — provenance, write-time auto-linking, contradiction resolution, secret-stripping, a full audit log, all native. The learning loops nobody else runs — predict-then-act surprise scoring, file-fight refactor detection, automatic failing-skill rewrites. The entire assembly substrate — durable resumable workflows, runtime self-extension, eval scorecards, agent-owned goals. Plus a custom domain at your own address. This is the half that makes next week's agent sharper than this week's.
**Where the rest of the field plays:** *distribution*. Hermes and OpenClaw go wide — messaging channels, deployment backends, model breadth, public skill hubs, wake-word voice. Real reach; worth knowing if reach is the only thing you're buying for. Nanobot is a deliberately minimal MCP host — a server plus an LLM, nothing more. None of them is built around the loops that make an agent get *better*.

### AGI-trajectory score

There is no standardized benchmark that scores agent *products* — ARC-AGI, GAIA and the like score *models* on tasks, and none of these four publish results. So this is a transparent rubric, scored straight off the verified table above. Every capability row that maps to **cognition trajectory** counts — durable structured memory, closed learning loops, self-assembly / self-improvement, autonomous operation (✅ = 1, ⚠️ = 0.5, ❌ = 0). It deliberately *excludes* the "how you reach it" rows — channels, voice, marketplace, open-source, LLM breadth — because those measure distribution, not how close to AGI.

| Dimension | Infinity | Hermes | OpenClaw | Nanobot |
|---|:-:|:-:|:-:|:-:|
| Durable structured memory · 7 rows | 7.0 | 2.5 | 3.0 | 0.0 |
| Closed learning loops · 7 rows | 7.0 | 2.5 | 2.0 | 0.0 |
| Self-assembly & self-improvement · 6 rows | 6.0 | 2.0 | 2.5 | 0.5 |
| Autonomous operation · 4 rows | 4.0 | 3.5 | 3.5 | 0.5 |
| **AGI-trajectory score** | **24.0 / 24** | **10.5 / 24** | **11.0 / 24** | **1.0 / 24** |
| | **100%** | **44%** | **46%** | **4%** |

Infinity scores 24/24 because durable memory, closed learning loops, and self-assembly *are* the spine it was built on — not features bolted onto a chat product. The rest of the field clears the bar for raw capability; Infinity is the one built for the *trajectory* — the loops that make it get better the longer you run it.

If you've already written skills for OpenClaw or Hermes, **drop them into `./skills/` and Infinity loads them unmodified.**

---

## What it can do for you

### Remembers everything that matters

Nothing said to Infinity disappears. Every conversation, every tool call, every decision is captured, structured, and searchable. When something contradicts what it knew before, the older fact is marked stale and the trail updates. When you correct it, every downstream memory updates with the correction.

Ask it *why does it think X* and it shows you the chain — the original observations, the decisions, the related projects, the dates. It can't bluff because it always carries the receipt.

**The tactics behind it:** parallel keyword + vector + graph retrieval (RRF k=60), session diversification, automatic associative linking at write time (A-MEM), cascading staleness through a typed memory graph, full audit log.

### Learns the way you do

Most "AI agents" quietly skip the hard part: they don't get smarter. Every session starts cold. Infinity doesn't — and the mechanism is the same one that makes *you* better at your job. You don't rewire your brain; you accumulate memory and skills over time, and that accumulation *is* the expertise. Infinity runs that loop on purpose.

After every session, a separate critic mind replays what happened, scores how it went, and writes down lessons. Those lessons feed forward — you don't coach it twice.

Before each tool call it writes down what it expects; after, it scores its own surprise. When reality keeps disagreeing with its gut, that becomes the next thing it studies — instead of confidently digging the same hole five times.

Every night, while you sleep, it cleans house: decays stale memories, prunes weak connections, resolves contradictions in its own head, re-weights its own skills by how well they've actually been working. You wake up to a sharper agent, not a noisier one.

Run it for a month and it is measurably better at *your* problems than it was on day one. That isn't a tagline — it's the architecture.

**The tactics behind it:** Park-style reflection trees with a MAR-pattern critic persona, JEPA-style predict-then-act surprise scoring, LightMem-style sleep-time consolidation. No competitor runs the full loop — the predict-then-act surprise scoring is Infinity's alone.

### Builds habits from watching you

Every session, Infinity notices the procedures you keep running and crystallises them into skills you can promote, edit, or reject. Repeated tool sequences across multiple sessions become candidate habits before you've even named them.

When you fight the same file three times in a session — multiple edits, failing builds — it drafts a refactor proposal so you can decide what to do about it. (It never makes the edit on its own; that still flows through your approval queue.)

Once a skill is approved, it becomes a first-class instinct: the agent reaches for it automatically before it goes hunting through tools.

**The tactics behind it:** Voyager-style session extraction, real-time tool-triplet discovery, source-fight detection, procedural-tier memory promotion (CoALA).

### Turns a sentence into a working system

Tell Infinity *"every weekday morning, pull my calendar, draft prep notes for each meeting, drop them on my dashboard — but check with me before booking anything,"* and it doesn't just do it once. It **assembles** it: writes the skill, wires the steps into a durable workflow, schedules it, and the result lands on your dashboard. The workflow survives restarts — if the server reboots mid-run, it resumes exactly where it left off. Steps that fail retry on their own. "Check with me" becomes a real checkpoint that pauses the whole thing and pings you.

When it hits a capability it doesn't have — an API it's never touched, a service it isn't wired to — it wires it in itself, at runtime, no redeploy. It writes new skills the moment it spots a reusable pattern, and they're usable immediately, not queued for later.

You're not configuring a tool. You're describing an outcome, and it builds the machine that produces it.

**The tactics behind it:** a generic surface contract every capability writes through, runtime skill-authoring straight into the live registry, a durable workflow engine (state machine + retries + human checkpoints, resumable across restarts), runtime MCP / REST-API self-extension. The competitors *execute what you wired* — Infinity assembles what you asked for.

### Holds goals — and knows if it's delivering

Infinity keeps its own goals, not just yours. *"Get the migration shipped," "keep the inbox triaged daily"* — it carries a living plan for each, records its own progress, and if one stalls or gets blocked, the heartbeat pulls it back into view so it re-plans instead of forgetting.

It also keeps score. Every workflow run, every skill, every tool it builds gets an outcome recorded; a scorecard rolls that into a success rate and a trend. When something it relies on starts regressing, that lands on your dashboard — *before* you'd have noticed.

And it knows when to interrupt. Urgent goes to your phone now; normal becomes a dashboard card; small stuff batches into a digest so it isn't pinging you all day. It tracks what it costs to run, so it can throttle expensive work instead of burning the budget blind.

**The tactics behind it:** agent-owned goals with an autonomous-pursuit heartbeat scan, a generic eval ledger with recent-vs-historical regression detection, urgency-routed notifications over Web Push, a cost ledger with budget rollup, and a structured world model of the people and projects you work with.

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
| **Substrate** | The assembly substrate — generic surface contract · runtime skill-authoring · durable workflow engine · runtime self-extension · eval scorecards · world model + agent goals · initiative + economics | ✅ |
| 8 | Voice — GPT Realtime over WebRTC, full-duplex, tool calls through the same gate as text | ✅ |

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
