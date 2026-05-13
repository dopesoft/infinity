# AGI loops — migration 011

This is the trail through the six AGI-trajectory loops that landed on top
of Infinity's Phase 4-7 substrate. Each is grounded in 2024-2026 research,
not vibes. The substrate is in place and runs automatically once the
binary boots against the migrated DB — no env flag required.

The full architectural detail lives in
[`ARCHITECTURE.md § 13`](../../ARCHITECTURE.md#13-agi-loops--migration-011).
This page is the table of contents + research citations + operational
overview.

## What shipped

| Loop | What it does | Citation |
|---|---|---|
| **Procedural memory tier (CoALA)** | Promoted skills materialize as `mem_memories` rows with `tier='procedural'`. Retrieval injects top-K into the system prompt at every turn. | [CoALA — Sumers et al., arXiv 2309.02427](https://arxiv.org/abs/2309.02427) (TMLR 2024) |
| **Reflection / metacognition** | `infinity reflect` walks recent sessions with a fresh "critic persona" Haiku call. Persists structured critique + lessons. Auto-promotes high-confidence lessons. | [MAR — arXiv 2512.20845](https://arxiv.org/html/2512.20845v1), [Generative Agents — Park 2304.03442](https://arxiv.org/abs/2304.03442), [Reflexion — Shinn 2303.11366](https://arxiv.org/abs/2303.11366) |
| **Predict-then-act** | PreToolUse writes an expected outcome to `mem_predictions`; PostToolUse resolves with a Jaccard surprise score. High surprise feeds curriculum + curiosity. | [V-JEPA 2 — arXiv 2506.09985](https://arxiv.org/abs/2506.09985), [LLM-JEPA — arXiv 2509.14252](https://arxiv.org/abs/2509.14252) (the *posture*, not the model) |
| **A-MEM auto-linking** | Every new memory writes top-4 `'associative'` edges to its cosine-nearest neighbours. Retrieval can traverse the graph, not just rank by score. | [A-MEM — arXiv 2502.12110](https://arxiv.org/pdf/2502.12110) |
| **Sleep-time consolidation** | `infinity consolidate` rebuilt as an 8-op offline regime: decay, hot-reset, cluster, contradiction resolution, associative pruning, weak-edge purge, procedural reweight, forget. | [LightMem — arXiv 2510.18866](https://arxiv.org/html/2510.18866v1), [Letta/MemGPT — arXiv 2310.08560](https://arxiv.org/abs/2310.08560) |
| **Curiosity gap-scan** | The heartbeat now scans for low-confidence memories, unresolved contradictions, uncovered graph mentions, high-surprise predictions. Writes idempotent `mem_curiosity_questions`. | [Generative Agents — arXiv 2304.03442](https://arxiv.org/abs/2304.03442), curiosity-driven exploration literature |
| **GEPA Pareto frontier** | Skill optimizer persists the WHOLE frontier (not a single champion) with `frontier_run_id` + `pareto_rank`. `SampleFromFrontier` draws weighted by score. | [GEPA — Agrawal et al., arXiv 2507.19457](https://arxiv.org/abs/2507.19457) (ICLR 2026 Oral) |
| **Voyager autotrigger** | Background ticker watches `mem_skill_runs` and auto-fires GEPA on skills past the failure threshold. Closes the failure → curriculum → skill → optimization cycle. | Inferred from Voyager ([arXiv 2305.16291](https://arxiv.org/abs/2305.16291)) — the close-the-loop step that paper assumed but didn't ship. |

## Schema (migration 011)

```sql
mem_reflections          (id, session_id, kind, critique, lessons jsonb,
                          quality_score, importance, embedding, created_at)

mem_predictions          (id, session_id, tool_call_id, tool_name, tool_input,
                          expected, actual, matched, surprise_score,
                          created_at, resolved_at)

mem_curiosity_questions  (id, question UNIQUE-WHEN-OPEN, rationale, source_kind,
                          source_ids uuid[], importance, status, answer,
                          asked_at, created_at, resolved_at)

mem_skill_proposals      + frontier_run_id uuid
                         + score float
                         + pareto_rank int
                         + gepa_metadata jsonb

mem_memories             + idx on (tier, status, strength DESC)
                           WHERE tier='procedural' AND status='active'

mem_relations            + idx on (relation_type)
```

All operations are idempotent. `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE
ADD COLUMN IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS` everywhere — safe
to re-run.

## Operational cadence

| Loop | Trigger | Cost (typical) |
|---|---|---|
| Procedural-memory upsert | On skill promotion (callback fired from `voyager.Manager.Decide`) | 1 embedder call |
| Procedural retrieval | Every agent turn (folded into `BuildSystemPrefix`) | 1 cosine search |
| Reflection | `infinity reflect` CLI / cron | ~$0.01 / session (Haiku) |
| Prediction record | Every PreToolUse hook | 0 LLM (Jaccard heuristic) |
| Prediction resolve | Every PostToolUse / PostToolUseFailure hook | 0 LLM |
| A-MEM auto-link | Every successful compression (async) | 1 vector query |
| Sleep-time consolidate | `infinity consolidate` CLI / cron | 0 LLM (pure SQL) |
| Curiosity scan | Every heartbeat tick (default 30m) | 0 LLM (pure SQL) |
| GEPA Pareto run | Voyager autotrigger or manual `POST /api/voyager/optimize` | ~$0.05–$0.20 (Haiku) |
| Voyager autotrigger | Background ticker, configurable cadence | 0 LLM (pure SQL until threshold hit) |

## Where each loop lives in the codebase

- **Procedural tier**: [`core/internal/memory/procedural.go`](../../core/internal/memory/procedural.go), attached via [`Searcher.AttachProcedural`](../../core/internal/memory/search.go), upserted from [`voyager.Manager.OnSkillPromoted`](../../core/internal/voyager/voyager.go) callback.
- **Reflection**: [`core/internal/memory/reflection.go`](../../core/internal/memory/reflection.go) + [`core/internal/llm/critic.go`](../../core/internal/llm/critic.go) + [`core/cmd/infinity/reflect.go`](../../core/cmd/infinity/reflect.go) (CLI).
- **Prediction**: [`core/internal/memory/predictions.go`](../../core/internal/memory/predictions.go) (store + Jaccard scorer) + [`core/internal/hooks/predict.go`](../../core/internal/hooks/predict.go) (PreToolUse/PostToolUse recorder).
- **A-MEM auto-linking**: in [`core/internal/memory/compress.go`](../../core/internal/memory/compress.go) (`autoLinkNeighbours`), fired async after every successful compression.
- **Sleep-time consolidate**: [`core/internal/memory/consolidate.go`](../../core/internal/memory/consolidate.go) — the 8-op `ConsolidateNightly`.
- **Curiosity scan**: [`core/internal/proactive/curiosity.go`](../../core/internal/proactive/curiosity.go) — composes into heartbeat via `ComposeChecklists` in [`serve.go`](../../core/cmd/infinity/serve.go).
- **GEPA Pareto frontier**: [`core/internal/voyager/optimizer.go`](../../core/internal/voyager/optimizer.go) — `paretoFrontier`, `insertFrontierProposal`, `SampleFromFrontier`.
- **Voyager autotrigger**: [`core/internal/voyager/autotrigger.go`](../../core/internal/voyager/autotrigger.go) — background ticker.

## Running the CLI loops

```sh
# Sleep-time consolidation (run nightly via cron):
infinity consolidate                # 8-op pure-SQL pass
infinity consolidate --compress     # also promote uncompressed observations
infinity consolidate --dry-run      # preview forget-pass deletions only

# Metacognition (run nightly via cron):
infinity reflect                                       # last 24h
infinity reflect --window 168h --limit 50              # last week, capped
infinity reflect --session <uuid>                      # single session
infinity reflect --session <uuid> --force              # overwrite existing
```

Both are safe to run repeatedly. `reflect` is idempotent on
`session_id` (skips sessions that already have a row unless `--force`).
`consolidate` is idempotent because every op is keyed on current state.

## Env vars added

| Var | Default | Meaning |
|---|---|---|
| `INFINITY_VOYAGER_AUTOTRIGGER` | `on` when `GEPA_URL` set | `off` to disable |
| `INFINITY_VOYAGER_AUTOTRIGGER_EVERY` | `30m` | tick interval |
| `INFINITY_VOYAGER_FAILURE_RATE` | `0.30` | failure threshold to fire GEPA |
| `INFINITY_VOYAGER_MIN_RUNS` | `5` | sliding window size per skill |
| `INFINITY_VOYAGER_COOLDOWN` | `6h` | per-skill cooldown after firing |

The other loops have no tunables — they activate automatically.

## What's not yet built (Studio surfaces)

The substrate is complete. The next layer is Studio rendering — none
require new endpoints:

- Reflections sub-tab on `/memory` rendering `mem_reflections` with
  quality_score colouring + lesson chips.
- Predictions feed on `/heartbeat` (or its own tab) sorted by
  surprise_score.
- Curiosity question approval / dismissal UI tied to
  `mem_curiosity_questions.status`.
- Procedural-tier badge on Memory list rows + dedicated filter.
- A-MEM graph visualization for top-K `'associative'` neighbours.
- Pareto frontier comparison view — render N candidates sharing a
  `frontier_run_id` side-by-side with promote/reject per row.

Backing data is all there in the schema; just needs the UI.
