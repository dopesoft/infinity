# Steal C — Per-Turn Effort Dial + Adversarial Verify Pass (corrected build spec)

Status: **design done, verifier-corrected, NOT yet built.** Source: design workflow `wf_d5b377da-c17`
(2026-06-26). Synthesize stage completed; rule-compliance verifier returned **revise** with 2 blockers
+ 4 issues (all confirmed against source). codefit/safety/UI verifiers + auto-revise were cut off by a
session limit (resets 6:50am CST) — **rerun them before merge.**

The boss's live model is GPT over the OAuth Codex backend; its thinking levels ARE Lever 2:
`none | low | medium | high | xhigh`. **The model id is sacred — only the compute that same model
spends varies.** See [[project_agi_steal_roadmap]] and [[feedback_respect_settings]].

## Architecture (1 paragraph)
One generic building block — a ctx-threaded effort hint (`llm.WithEffort`/`EffortFromContext`, an exact
clone of the proven `WithCacheKey` seam at `provider.go:221-240`) — plus a Loop-level resolver
(`resolveEffort`, mirroring `resolveActiveModel` at `loop.go:525`). A deterministic-first `effort`
package fuses signals that ALREADY exist (prior-turn surprise, LoopGate call-rate, coding/heavy flag,
nightly tool-error-rate, context fill) plus the already-running Gauge into one of the five levels. The
Loop stamps `WithEffort(streamCtx, level)` one line beside `WithCacheKey` at `loop.go:1318`; each
provider reads it INSIDE its existing reasoning gate. Lever 3 reuses the self-heal seam at `loop.go:1451`
to fire ONE bounded adversarial-verify pass on high/xhigh turns. A `ThinkingChip` (clone of `ModelChip`,
`ai-prompt-box.tsx:142`) gives Auto + pinned override; pins ride the WS frame like the `voice` flag;
Core echoes the auto-chosen level back via the existing gauge frame. Default ON but the floor is OMIT, so
ordinary chat costs exactly what it does today until a concrete signal escalates.

## The 6 verifier corrections (apply these to the recovered plan)

1. **[BLOCKER#1 — Codex effort enum] Do NOT default to a literal `none`; floor = OMIT.** The Codex
   backend's accepted effort vocabulary is unverified (`none` vs `minimal` are both valid + model-dependent
   per OpenAI docs, and the Codex backend isn't a published contract). Resolution that needs NO guess:
   - `llm.Effort` floor / ChatSettings default = empty string ⇒ provider **omits** the field ⇒ model default
     (today's behavior). Never send a literal default level.
   - **Build the effort-value 400-fallback that does not exist today:** when a Codex request 400s AND the
     body implicates the effort/reasoning param, retry once with the effort field omitted. Must
     **distinguish "effort rejected" from other 400s** (never-hide-errors rule) — match the body, don't
     blanket-omit. New seam alongside the model-id fallback at `openai_oauth.go:465-486`.
   - Treat the boss's configured `INFINITY_OPENAI_REASONING_EFFORT` as the authoritative spelling his
     backend accepts. Confirm accepted levels empirically via `railway logs --service core` (watch for
     effort-param 400s) during implementation — not by assumption.
   - Fix the stale comment at `openai_oauth.go:876` to state "model-dependent; omit === model default" —
     do NOT relabel `minimal` as deprecated.

2. **[BLOCKER#2 — fabricated const] Use the REAL surprise threshold.** There is no `curiositySurprise`
   const. The only value is `0.85`, hardcoded at `curiosity.go:290`. Either reuse `0.85` (cite it honestly,
   note it's not yet a shared const) or justify a new threshold on its own merits in `router_test.go`.
   Drop the false "0.7 / curiositySurprise" citation.

3. **[MAJOR — Rule #1b: no prompt in a .go const] Move the verify DIRECTIVE out of Go.** Keep the
   deterministic MECHANIC in Go (the gate: on high/xhigh + clean + non-trivial, run exactly one extra
   pass, capped, no stacking with self-heal). Move the "red-team your own answer" directive into a seeded
   **procedural-memory / skill body** (migration pattern mirroring `033_seed_*`), so it's versioned,
   visible in Studio, and improvable by Voyager/GEPA. The loop injects the recipe; it does not own it.

4. **[MAJOR — server-tracked-progress] The verify pass books a `mem_runs` row.** A second full LLM
   iteration costs real tokens/time and the boss may want to watch it. Wire it via `runs.Track` +
   `SetMeta(effort_tier, verify_passes)` so its in-flight state survives navigation/refresh — don't defer
   to a "future" aside (that's the spinner-evaporates regression).

5. **[MINOR — one authoritative emit] Pick ONE channel for the auto-chosen level.** Use the extended
   gauge frame (`wsGauge.AppliedEffort`) as the display source of truth; `EventEffort` carries only the
   live escalation + verify-pass signal. Add a test asserting `chip-displayed level == mem_turns.effort_tier`
   so the surfaces can't silently diverge. (EventKind enum is at `loop.go:~1043-1052`.)

6. **[MINOR — don't override the boss's Anthropic budget] Treat `ANTHROPIC_THINKING_BUDGET` as a
   ceiling.** The effort ladder scales WITHIN the boss-set budget, never above it; justify the budget
   numbers against Anthropic's extended-thinking docs (min 1024) rather than asserting a ladder. (Only
   relevant if the boss switches brain to Anthropic.)

## Difficulty classifier (`core/internal/effort/router.go`, deterministic, Rule #5)
`Router.Resolve(ctx, Inputs) -> (level, source)`, idempotent. Precedence:
1. boss pin wins (`source=boss_pinned`); 2. capability clamp `!modelSupportsReasoning` -> `("",unsupported)`;
3. deterministic base+bumps (coding floors `medium`; +1 tier each for prior-surprise ≥ **0.85**, tool-error-rate
> 0.25, call-rate ≥ 50% ceiling, context-fill > 0.6; clamp +2); 4. Gauge fallback ONLY when all
deterministic neutral (`glance→none/standard→low/deep→high`, reuses the async gauge — no new model call);
5. `xhigh` reserved for coding + stuck; 6. fail-open to `""` on any error.
Self-tuning thresholds in `setting.effort_thresholds` (no migration), nightly nudge via
`RegisterConsolidateHook` (`consolidate.go:24`), ≤10%/night, clamped.

## Lever 2 threading (the one new building block)
`provider.go`: add `Effort` enum + `WithEffort`/`EffortFromContext` (ctx value, mirrors `WithCacheKey` —
zero interface change, all ~9 non-loop callers + `noDashesProvider` pass through and never set it). Stamp at
`loop.go:1318`: `streamCtx := llm.WithEffort(llm.WithCacheKey(ctx, s.ID), perTurnEffort)`. Per-provider read
strictly inside the existing reasoning gate: openai_oauth ctx-first-then-env at `:888` (+ the #1 fallback);
anthropic `effortToBudget` scaled within the env ceiling (#6); openai api-key `reasoning_effort` guarded;
google no-op. Aux LLM calls (gauge/critic/summarizer/namer) run under their own ctx → never inherit effort
(assert with a no-leak test).

## Lever 3 (bounded verify pass)
Seam at `loop.go:1451` after the self-heal check inside `if len(resp.ToolCalls)==0`. New
`core/internal/agent/effort_pass.go`: `const maxVerifyPerTurn = 1`, `shouldVerify()`, `rehearsePass` no-op
extension point for steal B. Fires only on high/xhigh + clean + non-trivial + `!healedThisTurn`. Total
extra iterations/turn ≤ self-heal(2)+verify(1). Directive sourced from the seeded procedural/skill (#3),
not a Go const. Books a `mem_runs` row (#4).

## Composer UI
`studio/components/ui/ai-prompt-box.tsx` (the real `PromptInputBox`). `ThinkingChip` clones `ModelChip`
(`:142`) but opens a `DropdownMenu` (Auto + 5 levels) beside `<ModelChip>` at `:699`. Capability-gated
(shows `—`, disabled, on non-reasoning models). Label shows ACTIVE level (`"Auto · high"`). Per-turn pin
rides `useChat.send` opts → message/steer WS frame (mirror `voice` flag, `useChat.ts:1211/1161`) →
`ws.go wsClientMessage.Effort` → `WithEffort`. Display path: `wsGauge.AppliedEffort` (#5) → `useChat`
gauge case (`:1078`) → chip. Settings: `ChatSettings.DefaultEffort`+`EffortAuto` in `KeyChat` blob (no
migration), rendered in `ChatSettingsSection`. **375px Safari check required before shipping** (4th control
in the left cluster).

## Files, env, observability, tests
See the full recovered synthesize output (§6 file table, §7 env, §8 observability, §10 tests) preserved in
the workflow journal: `.../subagents/workflows/wf_d5b377da-c17/journal.jsonl` (agentId `ad40c6daf25dc3519`).
New migration `NNN_turn_effort.sql` adds `mem_turns.effort_tier`+`effort_source` (apply same session via
`infinity migrate`). Env: `INFINITY_EFFORT_POLICY=off` / `INFINITY_EFFORT_VERIFY=off` opt-outs; both
default ON but neutral (floor OMIT). Tests encode WHY each signal raises a tier + the no-effort-leak test.

## Before building (gates)
1. Resolve #1 in design as above (omit-floor + build the 400-fallback) — DONE in this spec.
2. Rerun codefit + safety + UI verifiers after the 6:50am CST session reset (they never ran).
3. Implement as testable batches: (a) `llm.WithEffort` block + tests; (b) `effort` package + tests;
   (c) provider reads + 400-fallback; (d) loop stamp + resolver; (e) Lever 3 + seeded directive; (f) UI.
   Do NOT edit the loop hot path while a session-limit cutoff is possible (risk of half-applied edits).
