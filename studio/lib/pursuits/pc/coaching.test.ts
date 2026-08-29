/**
 * Tests for the Psycho-Cybernetics coaching script.
 *
 * These encode WHY, per CLAUDE.md operating rule 9. Each block names the
 * regression it exists to catch; if the described behaviour goes away, the
 * assertion has to fail. In particular:
 *
 *   • Jarvis must never open with generic copy when the cockpit holds the
 *     boss's own words. The whole point of the redesign is that the opening
 *     line, the rehearsal scene and the proof action come from stored state.
 *   • A finished flow must produce the EXACT cockpit writes the old form
 *     produced. The answer keys are a contract with Go (`proof_pledge` is what
 *     becomes a proof row); renaming one silently stops persistence.
 *   • No flow may dead-end. A beat that neither asks nor offers is a
 *     conversation that strands the boss mid-session.
 *   • The phase is the server's decision. Re-deriving it on the client is how
 *     the cockpit and a seeded chat would disagree about what day it is.
 *
 * Fixtures are built from the REAL cockpit shape (lib/pursuits/pc/types.ts),
 * which mirrors core/internal/pursuits/pc/types.go one-for-one.
 */

import { describe, expect, it } from "vitest";

import {
  adjustmentLine,
  asClause,
  buildCommitWrites,
  buildScript,
  clip,
  describeProgress,
  openingFrame,
  reachableBeats,
  recommendProof,
  recommendScene,
  type CoachBeat,
  type CoachChoice,
} from "./coaching";
import type {
  PCCockpit,
  PCEvidence,
  PCMemory,
  PCPattern,
  PCPhase,
  PCProof,
} from "./types";

/* ── Fixtures ──────────────────────────────────────────────────────────── */

function cockpit(overrides: Partial<PCCockpit> = {}): PCCockpit {
  const state: PCCockpit["state"] = {
    pursuit_id: "p1",
    cycle_number: 1,
    cycle_length_days: 21,
    current_day: 5,
    cycle_started_at: "2026-08-24T00:00:00Z",
    missed_days_count: 0,
    current_identity: "Someone who quotes his real price without flinching",
    current_objective: "Three retainers signed by the end of the cycle",
    current_limiting_pattern: "Financial uncertainty",
    pressure_test: { fear: "", doubt: "", alternate: "" },
    timezone: "America/Chicago",
    created_at: "2026-08-24T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
    ...(overrides.state ?? {}),
  };

  return {
    pursuit: {
      id: "p1",
      title: "Psycho-Cybernetics",
      cadence: "daily",
      experience: "psycho_cybernetics",
      config: {},
      created_at: "2026-08-24T00:00:00Z",
    },
    today_proofs: [],
    recent_proofs: [],
    today_evidence: [],
    recent_evidence: [],
    memories: [],
    patterns: [],
    corrections: [],
    recent_sessions: [],
    cycle_reviews: [],
    guidance: {
      phase: "morning",
      headline: "Start morning rehearsal",
      body: "",
      hints: [],
      prompt: "",
    },
    ...overrides,
    state,
  };
}

function proof(over: Partial<PCProof> = {}): PCProof {
  return {
    id: "pr1",
    pursuit_id: "p1",
    label: "Send the proposal at full rate",
    cycle_number: 1,
    day_in_cycle: 3,
    planned_at: "2026-08-26T09:00:00Z",
    taken: false,
    note: "",
    created_at: "2026-08-26T09:00:00Z",
    updated_at: "2026-08-26T09:00:00Z",
    ...over,
  };
}

function evidence(over: Partial<PCEvidence> = {}): PCEvidence {
  return {
    id: "ev1",
    pursuit_id: "p1",
    kind: "resistance",
    body: "Discounted before they asked",
    tags: [],
    cycle_number: 1,
    day_in_cycle: 4,
    captured_at: "2026-08-27T15:00:00Z",
    ...over,
  };
}

function pattern(over: Partial<PCPattern> = {}): PCPattern {
  return {
    id: "pa1",
    pursuit_id: "p1",
    kind: "correction",
    body: "State the number and then stop talking",
    refs: {},
    cycle_number: 1,
    day_in_cycle: 4,
    created_at: "2026-08-27T22:00:00Z",
    ...over,
  };
}

function memory(over: Partial<PCMemory> = {}): PCMemory {
  return {
    id: "m1",
    pursuit_id: "p1",
    title: "The Kalman call",
    body: "Held the number, they said yes on the spot",
    tags: [],
    weight: 70,
    saved_at: "2026-08-25T00:00:00Z",
    ...over,
  };
}

/** Walk a script from `start`, always taking the first choice / answering
 *  every composer, and return the beat ids visited plus the answers collected.
 *  This is the "boss taps accept on everything" path. */
function walkAccepting(c: PCCockpit): {
  visited: string[];
  answers: Record<string, string>;
  commit: CoachBeat | undefined;
} {
  const script = buildScript(c);
  const visited: string[] = [];
  const answers: Record<string, string> = {};
  let id: string | undefined = script.start;
  let guard = 0;

  while (id && guard++ < 40) {
    const beat: CoachBeat | undefined = script.beats[id];
    if (!beat) break;
    visited.push(beat.id);
    if (beat.commit) return { visited, answers, commit: beat };

    if (beat.compose) {
      answers[beat.compose.key] = `answer:${beat.compose.key}`;
      id = beat.compose.next;
      continue;
    }
    const choice: CoachChoice | undefined = beat.choices?.[0];
    if (!choice) break;
    if (choice.record) answers[choice.record.key] = choice.record.value;
    if (choice.compose) {
      answers[choice.compose.key] = `answer:${choice.compose.key}`;
      id = choice.compose.next;
      continue;
    }
    id = choice.next;
  }
  return { visited, answers, commit: undefined };
}

/* ── The opening line ──────────────────────────────────────────────────── */

describe("openingFrame", () => {
  it("names the boss's own limiting pattern, not a generic frame", () => {
    // The rejected version of this surface opened with the same paragraph for
    // everyone. If this ever stops quoting stored state, the redesign is
    // undone regardless of how the pixels look.
    expect(openingFrame(cockpit())).toBe(
      "Today we are training your nervous system not to treat financial uncertainty as an emergency.",
    );
  });

  it("falls back to the objective when no pattern is named yet", () => {
    const c = cockpit({
      state: { ...cockpit().state, current_limiting_pattern: "" },
    });
    expect(openingFrame(c)).toContain("three retainers signed");
  });

  it("stays truthful rather than inventing material on an empty programme", () => {
    const c = cockpit({
      state: {
        ...cockpit().state,
        current_limiting_pattern: "",
        current_objective: "",
      },
    });
    // Generic here is CORRECT: there is nothing on file. What must never
    // happen is generic copy while real material exists.
    expect(openingFrame(c)).toBe(
      "Today we are training your nervous system to hold the identity when it is under pressure.",
    );
  });
});

describe("asClause / clip", () => {
  it("folds a stored sentence into mid-sentence position", () => {
    expect(asClause("Financial uncertainty.")).toBe("financial uncertainty");
  });

  it("leaves 'I' and acronyms alone so the line does not read broken", () => {
    expect(asClause("I flinch at the number")).toBe("I flinch at the number");
    expect(asClause("ARR anxiety")).toBe("ARR anxiety");
  });

  it("clips a pasted paragraph instead of speaking it whole", () => {
    const long = "x".repeat(400);
    expect(clip(long, 40)).toHaveLength(40);
    expect(clip(long, 40).endsWith("…")).toBe(true);
  });
});

describe("describeProgress", () => {
  it("reads as a day counter and hides cycle 1 and zero misses", () => {
    expect(describeProgress(cockpit())).toBe("day 5 of 21");
  });

  it("surfaces cycle and missed days once they carry information", () => {
    const c = cockpit({
      state: { ...cockpit().state, cycle_number: 3, missed_days_count: 2 },
    });
    expect(describeProgress(c)).toBe("day 5 of 21 · cycle 3 · 2 missed");
  });
});

/* ── Recommendations ───────────────────────────────────────────────────── */

describe("recommendScene", () => {
  it("prefers the pressure test, because rehearsing the crack is the method", () => {
    const c = cockpit({
      state: {
        ...cockpit().state,
        pressure_test: { fear: "The renewal call on Thursday", doubt: "", alternate: "" },
      },
      recent_evidence: [evidence()],
    });
    const rec = recommendScene(c);
    expect(rec.text).toContain("the renewal call on Thursday");
    expect(rec.because).not.toHaveLength(0);
  });

  it("falls back to the most recent resistance capture, with the day it happened", () => {
    const c = cockpit({ recent_evidence: [evidence(), evidence({ id: "ev2" })] });
    expect(recommendScene(c).text).toBe(
      "The situation from day 4: discounted before they asked.",
    );
  });

  it("ignores evidence rows when picking the scene, only resistance is a crack", () => {
    const c = cockpit({
      recent_evidence: [evidence({ kind: "evidence", body: "Held the price" })],
      corrections: [pattern()],
    });
    expect(recommendScene(c).text).toContain("state the number and then stop talking");
  });

  it("always gives a reason, so 'Why that one?' is never empty", () => {
    for (const c of [
      cockpit(),
      cockpit({ recent_evidence: [evidence()] }),
      cockpit({ corrections: [pattern()] }),
      cockpit({
        state: { ...cockpit().state, current_objective: "", current_limiting_pattern: "" },
      }),
    ]) {
      expect(recommendScene(c).because.length).toBeGreaterThan(20);
      expect(recommendScene(c).text.length).toBeGreaterThan(10);
    }
  });
});

describe("recommendProof", () => {
  it("re-offers a proof that was pledged and missed, framed as too big rather than failed", () => {
    const c = cockpit({ recent_proofs: [proof()] });
    const rec = recommendProof(c);
    expect(rec.text).toBe("Send the proposal at full rate");
    expect(rec.because).toContain("day 3");
    expect(rec.because).toContain("not a grade");
  });

  it("does not re-offer a proof already pledged today", () => {
    const today = proof({ id: "today1", label: "Today's pledge", day_in_cycle: 5 });
    const c = cockpit({ today_proofs: [today], recent_proofs: [today, proof()] });
    expect(recommendProof(c).text).toBe("Send the proposal at full rate");
  });

  it("prefers the boss's own correction over repeating a proof that landed", () => {
    const c = cockpit({
      recent_proofs: [proof({ taken: true })],
      corrections: [pattern()],
    });
    expect(recommendProof(c).text).toBe("State the number and then stop talking");
  });

  it("derives from the identity rather than going blank on a fresh programme", () => {
    expect(recommendProof(cockpit()).text).toContain(
      "quotes his real price without flinching",
    );
  });
});

/* ── The beat machine ──────────────────────────────────────────────────── */

describe("buildScript", () => {
  it("follows the server's phase and never re-derives it", () => {
    const phases: PCPhase[] = [
      "onboarding",
      "morning",
      "midday",
      "evening",
      "recovery",
      "review",
      "idle",
    ];
    for (const phase of phases) {
      const c = cockpit({ guidance: { ...cockpit().guidance, phase } });
      expect(buildScript(c).phase).toBe(phase);
    }
  });

  it("treats an unknown future phase as idle instead of rendering nothing", () => {
    // A phase added in Go before this file knows about it must degrade to a
    // readable surface, never to an empty screen.
    const c = cockpit({
      guidance: { ...cockpit().guidance, phase: "adjustment" },
    });
    expect(buildScript(c).beats[buildScript(c).start]).toBeDefined();
  });

  it("speaks one short message at a time, never an essay", () => {
    for (const phase of ["morning", "midday", "evening", "recovery", "review", "onboarding"] as PCPhase[]) {
      const script = buildScript(cockpit({ guidance: { ...cockpit().guidance, phase } }));
      for (const beat of reachableBeats(script)) {
        expect(beat.lines.length).toBeLessThanOrEqual(3);
        for (const line of beat.lines) {
          // The rejected surface put a four-sentence paragraph in a card. A
          // spoken line that long is the same failure in a chat bubble.
          expect(line.length).toBeLessThanOrEqual(220);
        }
      }
    }
  });

  it("never dead-ends: every reachable beat asks, offers, or commits", () => {
    for (const phase of [
      "onboarding",
      "morning",
      "midday",
      "evening",
      "recovery",
      "review",
      "idle",
    ] as PCPhase[]) {
      const script = buildScript(cockpit({ guidance: { ...cockpit().guidance, phase } }));
      for (const beat of reachableBeats(script)) {
        const terminal = beat.id === "done";
        const actionable =
          Boolean(beat.compose) || (beat.choices?.length ?? 0) > 0 || Boolean(beat.commit);
        expect(terminal || actionable).toBe(true);
      }
    }
  });

  it("routes every choice and composer at a beat that exists", () => {
    for (const phase of [
      "onboarding",
      "morning",
      "midday",
      "evening",
      "recovery",
      "review",
      "idle",
    ] as PCPhase[]) {
      const script = buildScript(cockpit({ guidance: { ...cockpit().guidance, phase } }));
      for (const beat of reachableBeats(script)) {
        if (beat.compose) expect(script.beats[beat.compose.next]).toBeDefined();
        for (const choice of beat.choices ?? []) {
          const target = choice.compose ? choice.compose.next : choice.next;
          expect(target, `${beat.id}/${choice.id}`).toBeDefined();
          expect(script.beats[target as string]).toBeDefined();
        }
      }
    }
  });

  it("offers the boss accept, adjust and explain on every recommendation", () => {
    // "Do not ask the user to invent the programme" cuts both ways: he must be
    // able to take the recommendation in one tap AND override it.
    const script = buildScript(cockpit());
    for (const id of ["scene", "proof"]) {
      const ids = (script.beats[id].choices ?? []).map((c) => c.id);
      expect(ids).toContain("accept");
      expect(ids).toContain("why");
      expect(script.beats[id].choices?.some((c) => c.compose)).toBe(true);
    }
  });

  it("puts the banked memory in front of him before the rehearsal, not after", () => {
    const c = cockpit({ rehearsal_memory: memory() });
    const script = buildScript(c);
    expect(script.beats.settle.choices?.[0].next).toBe("memory");
    expect(script.beats.memory.quote?.body).toContain("Held the number");
    expect(script.beats.memory.choices?.[0].next).toBe("scene");
  });

  it("skips the memory beat entirely when the bank is empty", () => {
    const script = buildScript(cockpit());
    expect(script.beats.memory).toBeUndefined();
    expect(script.beats.settle.choices?.[0].next).toBe("scene");
  });

  it("answers 'Help me picture it' rather than repeating the instruction", () => {
    const script = buildScript(cockpit());
    const help = script.beats.settle.choices?.find((c) => c.id === "help");
    expect(help?.next).toBe("settle_help");
    expect(script.beats.settle_help.lines.join(" ")).toContain("quiet room");
  });

  it("asks for a smaller proof when the day logged more resistance than evidence", () => {
    const c = cockpit({
      adjustment: {
        phase: "adjustment",
        headline: "Consider a smaller proof for tomorrow",
        body: "",
        hints: [],
        prompt: "",
      },
    });
    expect(buildScript(c).beats.proof.lines[0]).toContain("smaller");
    expect(adjustmentLine(c)).toContain("smaller correction");
  });

  it("says nothing about adjustment when the server did not raise one", () => {
    expect(adjustmentLine(cockpit())).toBe("");
  });
});

/* ── Persistence: the contract with Go ─────────────────────────────────── */

describe("buildCommitWrites", () => {
  it("writes a morning as one session carrying the rehearsal and the pledge", () => {
    // `proof_pledge` is what pc.Store.Apply promotes into a tracked proof row.
    // If this key drifts, the boss pledges a proof and no proof appears.
    const { answers, commit } = walkAccepting(cockpit());
    expect(commit?.commit?.sessionKind).toBe("morning");
    const writes = buildCommitWrites(commit!.commit!, answers);
    expect(writes).toHaveLength(1);
    expect(writes[0].action).toBe("session");
    expect(writes[0].body.kind).toBe("morning");
    expect(Object.keys(writes[0].body.answers ?? {})).toEqual(
      expect.arrayContaining(["rehearsal", "proof_pledge"]),
    );
  });

  it("carries the accepted recommendation through verbatim, not a placeholder", () => {
    const c = cockpit({
      state: {
        ...cockpit().state,
        pressure_test: { fear: "The renewal call", doubt: "", alternate: "" },
      },
      recent_proofs: [proof()],
    });
    const { answers } = walkAccepting(c);
    expect(answers.rehearsal).toBe(recommendScene(c).text);
    expect(answers.proof_pledge).toBe(recommendProof(c).text);
  });

  it("writes an evening with the four parts of the question kept apart", () => {
    const c = cockpit({ guidance: { ...cockpit().guidance, phase: "evening" } });
    const { answers, commit } = walkAccepting(c);
    const writes = buildCommitWrites(commit!.commit!, answers);
    expect(writes[0].body.kind).toBe("evening");
    expect(Object.keys(writes[0].body.answers ?? {}).sort()).toEqual([
      "correction",
      "fact",
      "interpretation",
      "lesson",
    ]);
  });

  it("writes midday captures under the keys that become evidence rows", () => {
    const c = cockpit({ guidance: { ...cockpit().guidance, phase: "midday" } });
    const { answers, commit } = walkAccepting(c);
    const writes = buildCommitWrites(commit!.commit!, answers);
    expect(Object.keys(writes[0].body.answers ?? {}).sort()).toEqual([
      "evidence",
      "resistance",
    ]);
  });

  it("closes a cycle through the review action, never as a plain session", () => {
    // A review routed as a session would log text and leave the cycle open
    // forever, because only CompleteReview increments the cycle.
    const c = cockpit({ guidance: { ...cockpit().guidance, phase: "review" } });
    const { answers, commit } = walkAccepting(c);
    const writes = buildCommitWrites(commit!.commit!, answers);
    expect(writes).toHaveLength(1);
    expect(writes[0].action).toBe("review");
    expect(writes[0].body.wins).toBe("answer:wins");
    expect(writes[0].body.next_identity).toBe("answer:next_identity");
  });

  it("saves onboarding as identity first, then the history record", () => {
    // Identity first so a failure on the second write still leaves the cycle
    // set up. This is the ordering PCOnboarding used and it must not regress.
    const c = cockpit({ guidance: { ...cockpit().guidance, phase: "onboarding" } });
    const { answers, commit } = walkAccepting(c);
    const writes = buildCommitWrites(commit!.commit!, answers);
    expect(writes.map((w) => w.action)).toEqual(["identity", "session"]);
    expect(writes[0].body.identity).toBe("answer:identity");
    expect(writes[0].body.objective).toBe("answer:objective");
    expect(writes[0].body.pattern).toBe("answer:limiting_pattern");
    expect(writes[0].body.pressure_test).toEqual({
      fear: "answer:pressure_fear",
      doubt: "answer:pressure_doubt",
      alternate: "answer:pressure_alternate",
    });
    expect(writes[1].body.kind).toBe("onboarding");
  });

  it("drops blank answers so an optional skip never overwrites stored state", () => {
    const writes = buildCommitWrites(
      { kind: "session", sessionKind: "midday" },
      { evidence: "held the price", resistance: "   " },
    );
    expect(writes[0].body.answers).toEqual({ evidence: "held the price" });
  });

  it("keeps a late idle capture on the evidence key so it lands as evidence", () => {
    const c = cockpit({ guidance: { ...cockpit().guidance, phase: "idle" } });
    const script = buildScript(c);
    const capture = script.beats.open.choices?.find((ch) => ch.id === "capture");
    expect(capture?.compose?.key).toBe("evidence");
    expect(script.beats[capture!.compose!.next].commit?.kind).toBe("session");
  });
});
