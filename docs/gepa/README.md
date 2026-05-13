# GEPA optimizer sidecar

A FastAPI service that wraps a Genetic-Pareto Prompt Evolution loop so
Infinity can have skill prompts *improve themselves* from execution traces.
Phase 1: SKILL.md optimization only.

Grounded in Agrawal et al., [arXiv 2507.19457](https://arxiv.org/abs/2507.19457)
(ICLR 2026 Oral) — reflective prompt evolution beats GRPO by avg 6%
(up to 20%) with **35× fewer rollouts** when you maintain a Pareto frontier
of candidates and sample stochastically instead of locking to a single
champion.

**What's new since the original sidecar:**
- **Pareto frontier persistence** — `RunOptimizer` now persists EVERY
  surviving candidate (not just one winner) into `mem_skill_proposals`
  sharing a `frontier_run_id` + per-candidate `pareto_rank` and `score`.
- **`SampleFromFrontier(skill)`** — weighted draw for runtime A/B on
  individual calls without permanent promotion.
- **Voyager autotrigger** — background ticker in `core/internal/voyager/
  autotrigger.go` watches `mem_skill_runs` and fires `/api/voyager/optimize`
  for any skill past the failure-rate threshold. **This is the close-the-
  loop step Voyager originally promised** — see `INFINITY_VOYAGER_*` env
  vars below.

## Why a sidecar

DSPy + the GEPA pattern are Python-native. Infinity's core is Go. We keep
the optimizer in its own container so:

- Core stays small and fast.
- Optimization runs are minutes-long; isolating them prevents stalls in the
  agent loop.
- The sidecar holds no state — it can crash, restart, or be wiped without
  losing anything.

## Deploy

### Railway (recommended)

The repo's `railway.toml` already registers a `gepa` service that builds
from `docker/gepa/Dockerfile`. To provision it:

```sh
railway add --service gepa --repo dopesoft/infinity
railway variables --service gepa --set ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"
railway up --service gepa  # or push to main and let auto-deploy run
```

In the Railway dashboard, confirm the service's **Root Directory** is set
to `docker/gepa` (the toml does this; double-check on first deploy).

Wire the URL into core (Railway internal networking — no public ingress):

```sh
railway variables --service core --set GEPA_URL=http://gepa.railway.internal:8090
```

### Local

```sh
docker build -t infinity-gepa docker/gepa/
docker run -p 8090:8090 -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY infinity-gepa
GEPA_URL=http://localhost:8090 go run ./cmd/infinity serve
```

## Use

The Voyager API exposes a `/api/voyager/optimize` endpoint:

```sh
curl -X POST $CORE/api/voyager/optimize \
  -H 'content-type: application/json' \
  -d '{ "skill": "weekly-standup-summary", "trace_limit": 20 }'
```

What happens:

1. Voyager pulls the last N runs of `weekly-standup-summary` from
   `mem_skill_runs` (failures + successes).
2. POSTs them to GEPA at `/optimize` with the current `SKILL.md`.
3. GEPA asks Haiku to summarise root causes of failure, mutates the
   prompt 6 times, scores each candidate, returns a Pareto-sorted list.
4. `paretoFrontier` applies hard gates per candidate (size ≤ 15KB, valid
   frontmatter, non-identical) and returns ALL viable candidates sorted
   by score desc.
5. Voyager assigns a fresh `frontier_run_id` (UUID) and inserts EVERY
   surviving candidate into `mem_skill_proposals` as `candidate` rows
   sharing the run id, each with its `pareto_rank` and `score`.
6. Response is `OptimizeResult{ frontier_run_id, skill_name, calls,
   candidates: [{proposal_id, score, pareto_rank, rationale, ...}] }`.
7. The boss reviews the frontier in Studio and promotes the winning
   rank(s) via the existing `/api/voyager/proposals/<id>/decide` path.
8. On promotion, Voyager writes the new `SKILL.md` to disk, reloads the
   registry, and fires the `OnSkillPromoted` callback → procedural-tier
   memory upsert.

## Autotrigger — close the loop

`voyager.NewAutoTrigger(manager, NewOptimizer())` runs a background ticker
that calls `RunOptimizer` automatically when a skill's recent failure rate
crosses the threshold. **Without this, GEPA only ran when someone POSTed
the endpoint manually.**

Tunables (all env):

| Var | Default | Meaning |
|---|---|---|
| `INFINITY_VOYAGER_AUTOTRIGGER` | `on` when `GEPA_URL` is set | `off` to disable explicitly |
| `INFINITY_VOYAGER_AUTOTRIGGER_EVERY` | `30m` | tick interval |
| `INFINITY_VOYAGER_FAILURE_RATE` | `0.30` | fire GEPA when recent failure rate ≥ this |
| `INFINITY_VOYAGER_MIN_RUNS` | `5` | sliding window size per skill |
| `INFINITY_VOYAGER_COOLDOWN` | `6h` | per-skill cooldown after firing |

Each tick logs the skills it fires and the resulting `frontier_run_id`.

## Hard gates (in `core/internal/voyager/optimizer.go`)

| Gate | Where | Behaviour |
|---|---|---|
| Empty candidate | `paretoFrontier` | reject |
| `len > 15KB` | `paretoFrontier` | reject |
| Missing `---` frontmatter | `paretoFrontier` | reject |
| Identical to original | `paretoFrontier` | reject as no-op |
| Trust queue approval | existing `mem_skill_proposals.status` flow | the boss decides per frontier entry |

## Cost

Per optimization run, with default budget (`max_calls=24`):

- 1 root-cause summary call
- ~6 mutation calls
- ~6 scoring calls
- ≈$0.05–$0.20 in Haiku tokens at current pricing.

Triggered automatically via the Voyager autotrigger when `GEPA_URL` is
set on the core service — see the Autotrigger section above. Manual
triggering via the HTTP endpoint still works for one-off runs.

## What this is NOT

- Not a full DSPy compiler — that's a much heavier dependency we haven't
  needed yet.
- Not a code-evolver — Phase 1 only mutates the SKILL.md (instructions).
  Phase 4 (Hermes's terminology) would mutate implementation files; not
  shipping that here.
- Not a runtime — skills still execute in `core/internal/skills/runner.go`.
  GEPA only proposes new prompts; it never runs them.
