/* The coaching script: what Jarvis says, in what order, derived from the
 * cockpit the server already computed.
 *
 * This module is deliberately PURE. It takes a cockpit and returns a small
 * state machine of beats; it never touches the network, the clock, or React.
 * That matters for two reasons:
 *
 *   • The phase decision, the rehearsal memory pick, and the adjustment note
 *     are MECHANICS and already live in Go (core/internal/pursuits/pc/coach.go).
 *     This file only decides how those facts are spoken, one line at a time.
 *   • Everything here is testable without a browser, which is where the
 *     regressions that matter (a beat that dead-ends, a commit that writes the
 *     wrong answer key, a recommendation that quietly goes generic) get caught.
 *
 * The answer keys below are not free choices. They mirror the keys
 * core/internal/pursuits/pc/coach.go asks for and write.go promotes, because a
 * `proof_pledge` answer is what becomes a tracked proof row and an `evidence`
 * answer is what becomes an evidence row. Renaming one here silently stops the
 * side effect on the server.
 */

import type {
  PCAction,
  PCCockpit,
  PCEvidence,
  PCPhase,
  PCProof,
  PCWriteRequest,
} from "./types";

/* ── The beat machine ──────────────────────────────────────────────────── */

/** A written answer Jarvis is waiting for. */
export type CoachCompose = {
  /** Answer key. Must match what the Go coach expects for this phase. */
  key: string;
  /** The question, shown above the input. Jarvis's words, not a form label. */
  label: string;
  placeholder: string;
  help?: string;
  /** Prefill, so "make it smaller" starts from what he was offered. */
  initialValue?: string;
  /** Beat to move to once answered. */
  next: string;
  /** When true the boss can move on without writing anything. */
  optional?: boolean;
};

/** A one-tap response. */
export type CoachChoice = {
  id: string;
  label: string;
  /** Record this answer when picked (the accept path). */
  record?: { key: string; value: string };
  /** Open the composer for a written answer instead (the adjust path). */
  compose?: CoachCompose;
  /** Beat to move to. Ignored when `compose` is set (the compose owns `next`). */
  next?: string;
};

/** What closes the flow, expressed as cockpit writes rather than prose. */
export type CoachCommit = {
  /** Cockpit action(s) this beat performs, in order. */
  kind: "session" | "review" | "onboarding" | "none";
  /** Session kind for `session` commits. */
  sessionKind?: string;
};

export type CoachBeat = {
  id: string;
  /** One short message per entry. Delivered one at a time, never as an essay. */
  lines: string[];
  /** Material to sit with, shown as a quote under the last line. */
  quote?: { label: string; body: string };
  choices?: CoachChoice[];
  compose?: CoachCompose;
  /** Terminal beat: performs the writes, then speaks `lines`. */
  commit?: CoachCommit;
};

export type CoachScript = {
  phase: PCPhase;
  /** Beat id to open on. */
  start: string;
  beats: Record<string, CoachBeat>;
};

/** A recommendation Jarvis makes, with the reason he'd give if asked. */
export type CoachRecommendation = {
  text: string;
  because: string;
};

/* ── Small text helpers ────────────────────────────────────────────────── */

/** Trim, drop trailing punctuation, and clip so a paragraph pasted into the
 *  identity field can still be spoken in one line. */
export function clip(raw: string, max = 160): string {
  const t = raw.trim().replace(/\s+/g, " ");
  if (t.length <= max) return t;
  return `${t.slice(0, max - 1).trimEnd()}…`;
}

/** Fold a stored sentence into mid-sentence position: no trailing full stop,
 *  and lower-cased unless the first word is "I" or an acronym. */
export function asClause(raw: string, max = 160): string {
  const t = clip(raw, max).replace(/[.!?\s]+$/, "");
  if (!t) return "";
  // "I" and acronyms keep their case; anything else folds to lower so it reads
  // as the middle of Jarvis's sentence rather than a pasted fragment.
  const first = t.split(/\s+/)[0] ?? "";
  if (first === first.toUpperCase()) return t;
  return t.charAt(0).toLowerCase() + t.slice(1);
}

function has(v: string | undefined | null): v is string {
  return typeof v === "string" && v.trim().length > 0;
}

function latest<T>(rows: T[]): T | undefined {
  return rows.length > 0 ? rows[0] : undefined;
}

/* ── Recommendations ───────────────────────────────────────────────────── */

/** The scene to rehearse this morning.
 *
 * Ordered by how much the programme already knows: the pressure test is the
 * boss's own answer to "where would this crack", so it beats a guess derived
 * from the objective. Rehearsing the crack IS the method; a generic scene is
 * the failure mode this ordering exists to avoid. */
export function recommendScene(cockpit: PCCockpit): CoachRecommendation {
  const { state } = cockpit;
  const fear = state.pressure_test?.fear ?? "";
  const resistance = latest(
    cockpit.recent_evidence.filter((e: PCEvidence) => e.kind === "resistance"),
  );
  const correction = latest(cockpit.corrections);

  if (has(fear)) {
    return {
      text: `The moment you said would test this hardest: ${asClause(fear)}.`,
      because:
        "You named that yourself as where the identity cracks. Rehearsing the crack is the whole point: your nervous system stops meeting it cold.",
    };
  }
  if (resistance) {
    return {
      text: `The situation from day ${resistance.day_in_cycle}: ${asClause(resistance.body)}.`,
      because:
        "That is the most recent place the old pattern actually ran. Running it again, handled well, gives the same situation a second take on file.",
    };
  }
  if (correction) {
    return {
      text: `The correction you set yourself: ${asClause(correction.body)}.`,
      because:
        "You wrote that correction on day " +
        correction.day_in_cycle +
        ". Rehearsing it is how it stops being a note and starts being a reflex.",
    };
  }
  if (has(state.current_objective)) {
    return {
      text: `A real moment today that moves you toward ${asClause(state.current_objective)}.`,
      because:
        "Nothing in the programme names a pressure point yet, so we rehearse against what you are aiming at. Pick the moment that is actually in your calendar, not a hypothetical one.",
    };
  }
  return {
    text: "The hardest conversation on your calendar today, handled as the person your identity says you are.",
    because:
      "The programme has no pressure test and no captures yet, so this is the honest default. Once you have logged a few days I will pick from your own material instead.",
  };
}

/** The one proof action for today.
 *
 * A proof is behaviour, so the ordering prefers something already phrased as an
 * action: a proof that was pledged and missed, then the correction he wrote for
 * himself, then a proof that landed and is worth repeating. */
export function recommendProof(cockpit: PCCockpit): CoachRecommendation {
  const earlier = cockpit.recent_proofs.filter(
    (p: PCProof) => !cockpit.today_proofs.some((t) => t.id === p.id),
  );
  const missed = earlier.find((p) => !p.taken);
  const landed = earlier.find((p) => p.taken);
  const correction = latest(cockpit.corrections);

  if (missed) {
    return {
      text: clip(missed.label),
      because:
        `You pledged that on day ${missed.day_in_cycle} and it did not land. In Maltz's framing a missed proof is not a grade, it is the servo saying the action was too big. Same action, smaller, is the correction.`,
    };
  }
  if (correction) {
    return {
      text: clip(correction.body),
      because: `That is the correction you wrote on day ${correction.day_in_cycle}. Today is when it gets done rather than noted.`,
    };
  }
  if (landed) {
    return {
      text: clip(landed.label),
      because:
        `That landed on day ${landed.day_in_cycle}. Repeating something that worked is how the identity accumulates evidence instead of one-off wins.`,
    };
  }
  if (has(cockpit.state.current_identity)) {
    return {
      text: `One deliberate thing today that only makes sense if this is true: ${asClause(cockpit.state.current_identity)}.`,
      because:
        "Nothing is on file yet, so the proof comes straight off the identity. Keep it small enough that you are certain to do it, that certainty is what does the work.",
    };
  }
  return {
    text: "One small, visible action today that the old pattern would have talked you out of.",
    because:
      "The programme has nothing banked yet. Anything concrete counts, as long as it is small enough that you will actually do it.",
  };
}

/* ── The opening line ──────────────────────────────────────────────────── */

/** What today is training, in one sentence, from the boss's own words.
 *  Exported because it is the line the whole surface is judged on. */
export function openingFrame(cockpit: PCCockpit): string {
  const { state } = cockpit;
  if (has(state.current_limiting_pattern)) {
    return `Today we are training your nervous system not to treat ${asClause(
      state.current_limiting_pattern,
      120,
    )} as an emergency.`;
  }
  if (has(state.current_objective)) {
    return `Today we are training your nervous system to act as though ${asClause(
      state.current_objective,
      120,
    )} is already within reach.`;
  }
  return "Today we are training your nervous system to hold the identity when it is under pressure.";
}

/** "day 5 of 21", plus cycle and missed days when they carry information. */
export function describeProgress(cockpit: PCCockpit): string {
  const { state } = cockpit;
  const parts = [`day ${state.current_day} of ${state.cycle_length_days}`];
  if (state.cycle_number > 1) parts.push(`cycle ${state.cycle_number}`);
  if (state.missed_days_count > 0) parts.push(`${state.missed_days_count} missed`);
  return parts.join(" · ");
}

/* ── Script builders, one per phase ────────────────────────────────────── */

const CLOSE_CHOICES: CoachChoice[] = [
  { id: "close", label: "That's me for now", next: "done" },
];

/** Terminal beat every flow lands on. Nothing is pending, nothing is asked. */
function doneBeat(lines: string[]): CoachBeat {
  return { id: "done", lines };
}

function morningScript(cockpit: PCCockpit): CoachScript {
  const scene = recommendScene(cockpit);
  const proof = recommendProof(cockpit);
  const memory = cockpit.rehearsal_memory;
  const afterSettle = memory ? "memory" : "scene";
  const adjustment = cockpit.adjustment;

  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: ["Morning.", openingFrame(cockpit)],
      choices: [
        { id: "ready", label: "Ready", next: "settle" },
        { id: "rough", label: "Rough start today", next: "rough" },
      ],
    },
    {
      id: "rough",
      lines: [
        "Then we make it smaller, we do not skip it.",
        "Two minutes counts as a day. A missed day is the only thing that costs you anything.",
      ],
      choices: [{ id: "ok", label: "Okay", next: "settle" }],
    },
    {
      id: "settle",
      lines: [
        "First, let's settle your body. Take three slow breaths.",
        "Now picture a private place where nobody needs anything from you. It can be real or imagined.",
      ],
      choices: [
        { id: "there", label: "I'm there", next: afterSettle },
        { id: "help", label: "Help me picture it", next: "settle_help" },
      ],
    },
    {
      id: "settle_help",
      lines: [
        "Maltz called it the quiet room. Four walls, one chair, the light the way you like it.",
        "It does not have to be a real place, only vivid enough to feel like somewhere. Sit in it for a few breaths.",
      ],
      choices: [{ id: "there", label: "I'm there", next: afterSettle }],
    },
  ];

  if (memory) {
    beats.push({
      id: "memory",
      lines: [
        `Before we rehearse, go back to this one: ${clip(memory.title, 90)}.`,
        "Get the feel of it again. The rehearsal works better on a nervous system that has just remembered winning.",
      ],
      quote: memory.body.trim() ? { label: "In your words", body: memory.body } : undefined,
      choices: [{ id: "got", label: "I have it", next: "scene" }],
    });
  }

  beats.push(
    {
      id: "scene",
      lines: ["Here is what I want you to rehearse.", scene.text],
      choices: [
        {
          id: "accept",
          label: "That's the one",
          record: { key: "rehearsal", value: scene.text },
          next: "rehearse",
        },
        { id: "why", label: "Why that one?", next: "scene_why" },
        {
          id: "other",
          label: "Something else today",
          compose: {
            key: "rehearsal",
            label: "Which situation are you rehearsing instead?",
            placeholder: "The real moment today you want to run through.",
            next: "rehearse",
          },
        },
      ],
    },
    {
      id: "scene_why",
      lines: [scene.because],
      choices: [
        {
          id: "accept",
          label: "Fine, that one",
          record: { key: "rehearsal", value: scene.text },
          next: "rehearse",
        },
        {
          id: "other",
          label: "Something else today",
          compose: {
            key: "rehearsal",
            label: "Which situation are you rehearsing instead?",
            placeholder: "The real moment today you want to run through.",
            next: "rehearse",
          },
        },
      ],
    },
    {
      id: "rehearse",
      lines: [
        "Run it now, from behind your own eyes. Not a film of yourself, the thing itself.",
        "Watch yourself handle it as the person the identity says you are. Sixty seconds is plenty.",
      ],
      choices: [{ id: "done", label: "Done", next: "proof" }],
    },
    {
      id: "proof",
      lines: [
        adjustment
          ? "Now one proof action, and keep it smaller than yesterday's."
          : "Now one proof action. Small enough that you are certain to do it, and only sensible if the identity is true.",
        proof.text,
      ],
      choices: [
        {
          id: "accept",
          label: "I'll do that",
          record: { key: "proof_pledge", value: proof.text },
          next: "commit",
        },
        { id: "why", label: "Why that one?", next: "proof_why" },
        {
          id: "smaller",
          label: "Make it smaller",
          compose: {
            key: "proof_pledge",
            label: "Shrink it until you are certain you will do it today.",
            placeholder: "The smallest version that still counts.",
            help: "A proof you skip teaches the old pattern. A tiny one you complete teaches the new one.",
            initialValue: proof.text,
            next: "commit",
          },
        },
      ],
    },
    {
      id: "proof_why",
      lines: [proof.because],
      choices: [
        {
          id: "accept",
          label: "I'll do that",
          record: { key: "proof_pledge", value: proof.text },
          next: "commit",
        },
        {
          id: "smaller",
          label: "Make it smaller",
          compose: {
            key: "proof_pledge",
            label: "Shrink it until you are certain you will do it today.",
            placeholder: "The smallest version that still counts.",
            initialValue: proof.text,
            next: "commit",
          },
        },
      ],
    },
    {
      id: "commit",
      commit: { kind: "session", sessionKind: "morning" },
      lines: [
        "Logged. The rehearsal and the proof are both on today's record.",
        "I will ask you what actually happened this evening. Go and do the small thing first.",
      ],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Good. I am here if you want to talk anything through."]),
  );

  return { phase: "morning", start: "open", beats: index(beats) };
}

function middayScript(cockpit: PCCockpit): CoachScript {
  const pending = cockpit.today_proofs.find((p) => !p.taken);
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        "Quick check in, while the day is still live.",
        pending
          ? `You pledged this morning: ${asClause(pending.label)}. Nothing is scored here either way.`
          : "Nothing here is scored. Both answers are just data for tonight.",
      ],
      choices: [{ id: "go", label: "Go on", next: "evidence" }],
    },
    {
      id: "evidence",
      lines: [
        "What has happened in the last few hours that looks like evidence for the identity?",
        "Small counts. A pause before the old reflex is evidence.",
      ],
      compose: {
        key: "evidence",
        label: "Evidence for the identity",
        placeholder: "A moment it held, however small.",
        next: "resistance",
      },
    },
    {
      id: "resistance",
      lines: ["And where did the old pattern try to run?"],
      compose: {
        key: "resistance",
        label: "Resistance you noticed",
        placeholder: "Where it showed up. Naming it is already the correction.",
        optional: true,
        next: "commit",
      },
    },
    {
      id: "commit",
      commit: { kind: "session", sessionKind: "midday" },
      lines: ["Both captured. Back to it, I will close the day with you tonight."],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Noted. Say the word if something else comes up."]),
  ];
  return { phase: "midday", start: "open", beats: index(beats) };
}

function eveningScript(cockpit: PCCockpit): CoachScript {
  const took = cockpit.today_proofs.filter((p) => p.taken).length;
  const pledged = cockpit.today_proofs.length;
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        "Evening. One question, in four parts.",
        pledged > 0
          ? `You pledged ${pledged} today and took ${took}. That is the record, not a verdict.`
          : "This is Maltz's feedback loop: the fact, then your reading of it, kept apart.",
      ],
      choices: [{ id: "go", label: "Go on", next: "fact" }],
    },
    {
      id: "fact",
      lines: [
        "What is the fact of what happened today with the identity?",
        "Plainly, the way a camera would have it. No reading of it yet.",
      ],
      compose: {
        key: "fact",
        label: "The fact",
        placeholder: "What actually happened, stated flat.",
        next: "interpretation",
      },
    },
    {
      id: "interpretation",
      lines: [
        "Now your interpretation of that fact, kept separate.",
        "Separate so you can revise the reading later without arguing with the fact.",
      ],
      compose: {
        key: "interpretation",
        label: "Your interpretation",
        placeholder: "How you are reading it tonight.",
        next: "lesson",
      },
    },
    {
      id: "lesson",
      lines: ["What did the day teach you? One line."],
      compose: {
        key: "lesson",
        label: "The lesson",
        placeholder: "The signal the day sent back.",
        optional: true,
        next: "correction",
      },
    },
    {
      id: "correction",
      lines: [
        "And the correction you will try tomorrow.",
        "This is the part I will hand back to you in the morning, so make it specific.",
      ],
      compose: {
        key: "correction",
        label: "Tomorrow's correction",
        placeholder: "The one adjustment for tomorrow's rehearsal.",
        optional: true,
        next: "commit",
      },
    },
    {
      id: "commit",
      commit: { kind: "session", sessionKind: "evening" },
      lines: [
        "Day closed. The correction is filed against the pattern, so it comes back to you tomorrow.",
        "Sleep on it. The servo does its work while you are not watching.",
      ],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Goodnight. I will open with your correction in the morning."]),
  ];
  return { phase: "evening", start: "open", beats: index(beats) };
}

function recoveryScript(cockpit: PCCockpit): CoachScript {
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        "There you are.",
        "A day went by. There is no restart and nothing to make up, we pick it up from here.",
      ],
      choices: [{ id: "go", label: "Go on", next: "reason" }],
    },
    {
      id: "reason",
      lines: ["What pulled you away? One line, and no judgement in it."],
      compose: {
        key: "reason",
        label: "What pulled you away",
        placeholder: "Plainly. This is data for the servo, not a confession.",
        next: "smallest",
      },
    },
    {
      id: "smallest",
      lines: [
        "Now the smallest morning you are certain to finish today.",
        openingFrame(cockpit),
      ],
      compose: {
        key: "smallest_next_step",
        label: "The smallest rehearsal you will actually do now",
        placeholder: "A shorter version of the morning. Two minutes is a real answer.",
        next: "commit",
      },
    },
    {
      id: "commit",
      commit: { kind: "session", sessionKind: "recovery" },
      lines: ["Logged, and the streak is not the point. You are back in the cycle."],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Good. Same time tomorrow and we are properly running again."]),
  ];
  return { phase: "recovery", start: "open", beats: index(beats) };
}

function reviewScript(cockpit: PCCockpit): CoachScript {
  const { state } = cockpit;
  const taken = cockpit.recent_proofs.filter((p) => p.taken).length;
  const pledged = cockpit.recent_proofs.length;
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        `That is ${state.cycle_length_days} days.`,
        pledged > 0
          ? `${taken} of ${pledged} proof actions landed. Let's decide the next cycle deliberately rather than drifting into it.`
          : "Let's decide the next cycle deliberately rather than drifting into it.",
      ],
      choices: [{ id: "go", label: "Go on", next: "wins" }],
    },
    {
      id: "wins",
      lines: ["What did this cycle actually produce? Concrete things only."],
      compose: {
        key: "wins",
        label: "Wins",
        placeholder: "What is different now that was not before.",
        next: "misses",
      },
    },
    {
      id: "misses",
      lines: ["And what did not land?"],
      compose: {
        key: "misses",
        label: "Misses",
        placeholder: "The specific misses, held apart from any judgement.",
        optional: true,
        next: "next_identity",
      },
    },
    {
      id: "next_identity",
      lines: [
        `The identity you have been running is: ${asClause(state.current_identity, 120)}.`,
        "Keep it or change it. Leaving this blank keeps it.",
      ],
      compose: {
        key: "next_identity",
        label: "Next cycle's identity",
        placeholder: "Blank keeps the current one.",
        optional: true,
        next: "next_objective",
      },
    },
    {
      id: "next_objective",
      lines: ["Same question for the objective."],
      compose: {
        key: "next_objective",
        label: "Next cycle's objective",
        placeholder: "Blank keeps the current one.",
        optional: true,
        next: "next_pattern",
      },
    },
    {
      id: "next_pattern",
      lines: ["And the pattern you want to rehearse a correction against next."],
      compose: {
        key: "next_pattern",
        label: "Next cycle's limiting pattern",
        placeholder: "Blank keeps the current one.",
        optional: true,
        next: "commit",
      },
    },
    {
      id: "commit",
      commit: { kind: "review" },
      lines: [
        "Cycle closed and the next one starts at day one.",
        "Come back in the morning and we begin again.",
      ],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Well run. I will open the new cycle with you tomorrow."]),
  ];
  return { phase: "review", start: "open", beats: index(beats) };
}

function onboardingScript(cockpit: PCCockpit): CoachScript {
  const { state } = cockpit;
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        "Right. Let's set this up properly, it takes a few minutes and then it runs itself.",
        `${state.cycle_length_days} days, three short moments a day. I need three things from you first, in your own words.`,
      ],
      choices: [{ id: "go", label: "Go on", next: "objective" }],
    },
    {
      id: "objective",
      lines: [
        "What are you actually aiming at over the next few weeks?",
        "Concrete enough that you would know it had happened. Maltz's whole method needs a target the mind can picture.",
      ],
      compose: {
        key: "objective",
        label: "The objective",
        placeholder: "The outcome you are aiming at.",
        initialValue: state.current_objective,
        next: "pattern",
      },
    },
    {
      id: "pattern",
      lines: [
        "Now the thing that pulls you back.",
        "In Maltz's language that is the old self image talking. Naming it plainly is what lets us rehearse against it.",
      ],
      compose: {
        key: "limiting_pattern",
        label: "The limiting pattern",
        placeholder: "The reflex or the story you keep catching.",
        initialValue: state.current_limiting_pattern,
        next: "identity",
      },
    },
    {
      id: "identity",
      lines: [
        "Last one. Who are you practising being?",
        "Write it as behaviour someone could watch you do. It is an experiment for this cycle, not a claim about you.",
      ],
      compose: {
        key: "identity",
        label: "The operating identity",
        placeholder: "The person you are trying on.",
        initialValue: state.current_identity,
        next: "pressure",
      },
    },
    {
      id: "pressure",
      lines: [
        "Good. Now we pressure test it, so we find where it cracks here rather than in the moment.",
        "Where would this identity crack under pressure this week?",
      ],
      compose: {
        key: "pressure_fear",
        label: "Where it would crack",
        placeholder: "The situation that would test it hardest.",
        help: "This is the one I will hand back to you as tomorrow's rehearsal scene.",
        initialValue: state.pressure_test?.fear ?? "",
        optional: true,
        next: "doubt",
      },
    },
    {
      id: "doubt",
      lines: ["What part of it do you not fully believe yet?"],
      compose: {
        key: "pressure_doubt",
        label: "The honest doubt",
        placeholder: "Kept as data, not a verdict.",
        initialValue: state.pressure_test?.doubt ?? "",
        optional: true,
        next: "alternate",
      },
    },
    {
      id: "alternate",
      lines: ["And which identity did you almost choose instead?"],
      compose: {
        key: "pressure_alternate",
        label: "The one you almost chose",
        placeholder: "Worth keeping for the cycle review.",
        initialValue: state.pressure_test?.alternate ?? "",
        optional: true,
        next: "commit",
      },
    },
    {
      id: "commit",
      commit: { kind: "onboarding" },
      lines: [
        "That is the cycle set. Day one starts now.",
        "Come back in the morning and we will rehearse against the exact place you said it would crack.",
      ],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Set. I will open the first rehearsal with you tomorrow morning."]),
  ];
  return { phase: "onboarding", start: "open", beats: index(beats) };
}

function idleScript(cockpit: PCCockpit): CoachScript {
  const evidence = cockpit.today_evidence.length;
  const beats: CoachBeat[] = [
    {
      id: "open",
      lines: [
        "Morning, midday and evening are all logged. Today is done.",
        evidence > 0
          ? `${evidence} captured today. If something else surfaces late, put it on the record and I will use it tomorrow.`
          : "If something surfaces late, put it on the record and I will use it tomorrow.",
      ],
      choices: [
        {
          id: "capture",
          label: "Capture something",
          compose: {
            key: "evidence",
            label: "What happened?",
            placeholder: "A late moment the identity held.",
            next: "capture_commit",
          },
        },
        { id: "close", label: "Nothing else", next: "done" },
      ],
    },
    {
      id: "capture_commit",
      commit: { kind: "session", sessionKind: "midday" },
      lines: ["On the record. That goes into tomorrow's rehearsal material."],
      choices: CLOSE_CHOICES,
    },
    doneBeat(["Rest. Tomorrow we go again."]),
  ];
  return { phase: "idle", start: "open", beats: index(beats) };
}

function index(beats: CoachBeat[]): Record<string, CoachBeat> {
  const out: Record<string, CoachBeat> = {};
  for (const b of beats) out[b.id] = b;
  return out;
}

/** Build the whole guided session for whatever phase the server chose.
 *
 * The phase is NOT re-derived here. `cockpit.guidance.phase` is the coach's
 * decision, made in Go against the pursuit's own timezone; second-guessing it
 * on the client is how the cockpit and a seeded chat would start disagreeing
 * about what day it is. */
export function buildScript(cockpit: PCCockpit): CoachScript {
  switch (cockpit.guidance.phase) {
    case "onboarding":
      return onboardingScript(cockpit);
    case "morning":
      return morningScript(cockpit);
    case "midday":
      return middayScript(cockpit);
    case "evening":
      return eveningScript(cockpit);
    case "recovery":
      return recoveryScript(cockpit);
    case "review":
      return reviewScript(cockpit);
    default:
      return idleScript(cockpit);
  }
}

/** The adjustment note, spoken rather than boxed. Empty when there is none. */
export function adjustmentLine(cockpit: PCCockpit): string {
  const a = cockpit.adjustment;
  if (!a) return "";
  return `${a.headline}. Today logged more resistance than evidence, which in Maltz's framing is the servo asking for a smaller correction, not a failure.`;
}

/* ── Commit → cockpit writes ───────────────────────────────────────────── */

export type CoachWrite = { action: PCAction; body: PCWriteRequest };

/** Turn a finished flow into the exact cockpit writes that persist it.
 *
 * Pure on purpose: this is the seam where a renamed answer key or a dropped
 * write would silently stop persisting, so it is the thing the tests pin. The
 * derived side effects (a `proof_pledge` becoming a proof row, an `evidence`
 * answer becoming an evidence row, a `correction` becoming a pattern) are
 * enforced inside pc.Store.Apply and are deliberately NOT duplicated here. */
export function buildCommitWrites(
  commit: CoachCommit,
  answers: Record<string, string>,
): CoachWrite[] {
  const pick = (k: string) => (answers[k] ?? "").trim();
  const kept: Record<string, string> = {};
  for (const [k, v] of Object.entries(answers)) {
    const t = v.trim();
    if (t) kept[k] = t;
  }

  switch (commit.kind) {
    case "session":
      return [
        {
          action: "session",
          body: { kind: commit.sessionKind ?? "midday", answers: kept },
        },
      ];

    case "review":
      return [
        {
          action: "review",
          body: {
            wins: pick("wins"),
            misses: pick("misses"),
            next_identity: pick("next_identity"),
            next_objective: pick("next_objective"),
            next_pattern: pick("next_pattern"),
          },
        },
      ];

    case "onboarding":
      // Identity first so the state carries the answers even if the second
      // write fails; the session row is the history record of how the cycle
      // opened. Blank fields are left untouched server-side.
      return [
        {
          action: "identity",
          body: {
            identity: pick("identity"),
            objective: pick("objective"),
            pattern: pick("limiting_pattern"),
            pressure_test: {
              fear: pick("pressure_fear"),
              doubt: pick("pressure_doubt"),
              alternate: pick("pressure_alternate"),
            },
          },
        },
        { action: "session", body: { kind: "onboarding", answers: kept } },
      ];

    default:
      return [];
  }
}

/** Every beat reachable from `start`. Used by the tests to prove no flow can
 *  dead-end on a beat that asks for nothing and offers nothing. */
export function reachableBeats(script: CoachScript): CoachBeat[] {
  const seen = new Set<string>();
  const out: CoachBeat[] = [];
  const walk = (id: string) => {
    if (seen.has(id)) return;
    const beat = script.beats[id];
    if (!beat) return;
    seen.add(id);
    out.push(beat);
    if (beat.compose) walk(beat.compose.next);
    for (const c of beat.choices ?? []) {
      if (c.compose) walk(c.compose.next);
      else if (c.next) walk(c.next);
    }
  };
  walk(script.start);
  return out;
}
