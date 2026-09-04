/**
 * liveness.ts - is the turn alive, and what is it doing right now?
 *
 * PURE, and in `lib/chat` rather than inside `useChat`, for the same reason
 * `settle.ts` and `preserve.ts` are: no React, no timers, so the rules can be
 * tested in plain node instead of asserted in a comment.
 *
 * WHAT THIS REPLACES. A 90-second "no frame → the agent went silent" watchdog
 * that painted an error, dropped the stream state and force-closed a healthy
 * socket - on a turn that was merely thinking (2026-09-04: DeepSeek reasoned
 * for 2m40s, the browser declared it dead at 90s, the reply landed three
 * minutes later and was only ever seen by refreshing). The client was
 * guessing at the server's state from the shape of what it had received.
 *
 * Now the server SAYS. Every turn frame carries a `seq`; a `heartbeat` lands
 * every few seconds while a turn runs, naming its phase; `turn_status` is the
 * server's own answer to "is anything running here?". This module folds those
 * into one state and decides the only three things the client may do on its
 * own: ask for a replay (`attach`), rebuild the socket (`reconnect`), or, once
 * the server has said the turn is over, settle from the transcript
 * (`reconcile`). It never decides that a turn is dead, and it never produces
 * an error card.
 */

import type { TurnPhase, WSEvent, WSTurnStatus } from "@/lib/ws/client";

/** No frame of any kind for this long → ask the server what we missed. */
export const QUIET_MS = 20_000;
/** Still nothing after asking → the socket is the problem; rebuild it. */
export const DEAD_MS = 45_000;
/** Against an old core that does not answer `attach`, poll the transcript. */
export const LEGACY_POLL_MS = 30_000;

export type LivenessState = {
  /** The server said a turn is running (or we started one and it has not ended). */
  inFlight: boolean;
  turnId: string | null;
  /** Newest journaled seq seen for this session. 0 = nothing yet. */
  lastSeq: number;
  /** When the last frame of any kind (heartbeat included) arrived. */
  lastFrameAt: number;
  /** When the turn started, from the server when it told us, else our send. */
  startedAt: number;
  phase: TurnPhase | null;
  toolName?: string;
  thinkingTokens?: number;
  /** When we last asked for a replay on this silence; null once a frame lands. */
  attachSentAt: number | null;
  /** When we last rebuilt the socket on this silence; null once a frame lands. */
  reconnectSentAt: number | null;
  /** Stop was pressed and the server has not confirmed yet. */
  stopping: boolean;
  /** null until the server answers (or refuses) an attach. */
  attachSupported: boolean | null;
  /** Last legacy transcript poll, for the old-core fallback. */
  legacyPolledAt: number;
};

export type LivenessAction =
  | { kind: "none" }
  | { kind: "attach"; sinceSeq: number }
  | { kind: "reconnect" }
  /** The server said the turn is over (or the ring lost our head): rebuild from the transcript. */
  | { kind: "reconcile"; stopReason?: string };

export function initialLiveness(): LivenessState {
  return {
    inFlight: false,
    turnId: null,
    lastSeq: 0,
    lastFrameAt: 0,
    startedAt: 0,
    phase: null,
    attachSentAt: null,
    reconnectSentAt: null,
    stopping: false,
    attachSupported: null,
    legacyPolledAt: 0,
  };
}

/** The client sent a message: a turn is in flight from our point of view. */
export function onSend(s: LivenessState, now: number): LivenessState {
  return {
    ...s,
    inFlight: true,
    startedAt: now,
    lastFrameAt: now,
    phase: "starting",
    toolName: undefined,
    thinkingTokens: undefined,
    attachSentAt: null,
    reconnectSentAt: null,
    stopping: false,
  };
}

/** The boss pressed Stop. The label says so until the server confirms. */
export function onStop(s: LivenessState): LivenessState {
  return s.inFlight ? { ...s, stopping: true, phase: "stopping" } : s;
}

/** The socket came back (or opened): ask what we missed. */
export function onConnected(s: LivenessState): LivenessAction {
  return { kind: "attach", sinceSeq: s.lastSeq };
}

/** Switching conversations forgets everything about the old one. */
export function onSessionSwitch(s: LivenessState): LivenessState {
  return { ...initialLiveness(), attachSupported: s.attachSupported };
}

const TURN_FRAMES = new Set([
  "delta",
  "thinking",
  "tool_call",
  "tool_input_delta",
  "tool_result",
  "complete",
  "error",
  "effort",
  "steer_received",
]);

function isTurnFrame(ev: WSEvent): boolean {
  return TURN_FRAMES.has(ev.type);
}

function statusOf(ev: WSEvent): WSTurnStatus | null {
  if (ev.type === "turn_status" || ev.type === "heartbeat") return ev.turn_status;
  return null;
}

/**
 * onFrame folds one server frame into the state and says what, if anything,
 * to do about it.
 *
 *  • Any turn frame or heartbeat is proof of life: the silence timers reset.
 *  • A `seq` past the one we expected is a gap; ask for the replay.
 *  • `turn_status` is the server's word: in flight → we are streaming, from
 *    its start time; not in flight while we thought we were → reconcile.
 *  • `error{unknown type: attach}` is an old core: fall back to polling.
 *
 * It returns `duplicate: true` for a frame the transcript already has (a
 * replayed seq at or below the last one seen) so the caller can skip
 * rendering it without this module knowing anything about rendering.
 */
export function onFrame(
  s: LivenessState,
  ev: WSEvent,
  now: number,
): { state: LivenessState; action: LivenessAction; duplicate: boolean } {
  const none: LivenessAction = { kind: "none" };

  // Old core answering an attach it does not know.
  if (ev.type === "error" && typeof ev.message === "string" && ev.message.startsWith("unknown type: attach")) {
    return { state: { ...s, attachSupported: false, legacyPolledAt: now }, action: none, duplicate: true };
  }

  const st = statusOf(ev);
  if (st) {
    const next: LivenessState = {
      ...s,
      attachSupported: true,
      lastFrameAt: now,
      attachSentAt: null,
      reconnectSentAt: null,
      lastSeq: Math.max(s.lastSeq, st.seq ?? 0),
      thinkingTokens: st.thinking_tokens ?? s.thinkingTokens,
    };
    if (st.in_flight) {
      next.inFlight = true;
      next.turnId = st.turn_id ?? s.turnId;
      next.phase = (st.phase as TurnPhase) || s.phase || "starting";
      next.toolName = st.tool_name || undefined;
      const started = st.started_at ? Date.parse(st.started_at) : NaN;
      if (Number.isFinite(started) && started > 0) next.startedAt = started;
      if (next.phase !== "stopping") next.stopping = false;
      // A replay floor above what we have means the ring lost our head:
      // the transcript is the only complete copy of the start of the turn.
      if (ev.type === "turn_status" && s.lastSeq > 0 && (st.oldest_seq ?? 0) > s.lastSeq + 1) {
        return { state: next, action: { kind: "reconcile" }, duplicate: false };
      }
      // A heartbeat whose seq is ahead of ours means frames went missing.
      if (ev.type === "heartbeat" && (st.seq ?? 0) > s.lastSeq && s.lastSeq > 0) {
        return { state: { ...next, lastSeq: s.lastSeq, attachSentAt: now }, action: { kind: "attach", sinceSeq: s.lastSeq }, duplicate: false };
      }
      return { state: next, action: none, duplicate: false };
    }
    // The server says nothing is running.
    const wasStreaming = s.inFlight;
    next.inFlight = false;
    next.phase = null;
    next.toolName = undefined;
    next.stopping = false;
    return {
      state: next,
      action: wasStreaming ? { kind: "reconcile", stopReason: st.stop_reason } : none,
      duplicate: false,
    };
  }

  if (!isTurnFrame(ev)) return { state: s, action: none, duplicate: false };

  const seq = "seq" in ev && typeof ev.seq === "number" ? ev.seq : 0;
  const turnId = "turn_id" in ev && typeof ev.turn_id === "string" ? ev.turn_id : null;
  // A replayed or re-delivered frame we already rendered.
  if (seq > 0 && seq <= s.lastSeq && (!turnId || turnId === s.turnId)) {
    return { state: { ...s, lastFrameAt: now, attachSentAt: null, reconnectSentAt: null }, action: none, duplicate: true };
  }
  let action: LivenessAction = none;
  // A gap in the sequence: frames between were lost on the wire.
  if (seq > 0 && s.lastSeq > 0 && seq > s.lastSeq + 1 && (!turnId || turnId === s.turnId) && s.attachSentAt === null) {
    action = { kind: "attach", sinceSeq: s.lastSeq };
  }
  const next: LivenessState = {
    ...s,
    lastFrameAt: now,
    attachSentAt: action.kind === "attach" ? now : null,
    reconnectSentAt: null,
    lastSeq: seq > 0 ? Math.max(s.lastSeq, seq) : s.lastSeq,
    turnId: turnId ?? s.turnId,
  };
  switch (ev.type) {
    case "thinking":
      next.inFlight = true;
      next.phase = "thinking";
      next.toolName = undefined;
      if (typeof ev.thinking_tokens === "number") {
        next.thinkingTokens = Math.max(s.thinkingTokens ?? 0, ev.thinking_tokens);
      }
      break;
    case "delta":
      next.inFlight = true;
      next.phase = "streaming";
      next.toolName = undefined;
      break;
    case "tool_call":
      if (!ev.tool_call.nested) {
        next.inFlight = true;
        next.phase = ev.tool_call.awaiting_approval ? "awaiting_approval" : "tool";
        next.toolName = ev.tool_call.name;
      }
      break;
    case "tool_input_delta":
      next.inFlight = true;
      next.phase = "tool";
      next.toolName = ev.tool_input_delta.name || s.toolName;
      break;
    case "tool_result":
      if (!ev.tool_result.nested) {
        next.inFlight = true;
        next.phase = "thinking";
        next.toolName = undefined;
      }
      break;
    case "steer_received":
      next.phase = s.inFlight ? "steering" : s.phase;
      break;
    case "complete":
    case "error":
      next.inFlight = false;
      next.phase = null;
      next.toolName = undefined;
      next.stopping = false;
      break;
    default:
      break;
  }
  if (next.stopping && next.inFlight) next.phase = "stopping";
  return { state: next, action, duplicate: false };
}

/**
 * onTick runs once a second while a turn is in flight and decides whether
 * the silence has gone on long enough to act. It escalates exactly once per
 * stage: one attach, then one reconnect, then (after another DEAD_MS) another
 * reconnect - never an error.
 */
export function onTick(s: LivenessState, now: number): { state: LivenessState; action: LivenessAction } {
  const none: LivenessAction = { kind: "none" };
  if (!s.inFlight) return { state: s, action: none };
  if (s.attachSupported === false) {
    // Old core: the transcript is the only truth we can ask for.
    if (now - s.legacyPolledAt >= LEGACY_POLL_MS) {
      return { state: { ...s, legacyPolledAt: now }, action: { kind: "reconcile" } };
    }
    return { state: s, action: none };
  }
  const quiet = now - Math.max(s.lastFrameAt, s.startedAt);
  if (quiet < QUIET_MS) return { state: s, action: none };
  if (s.attachSentAt === null) {
    return { state: { ...s, attachSentAt: now }, action: { kind: "attach", sinceSeq: s.lastSeq } };
  }
  if (quiet >= DEAD_MS && (s.reconnectSentAt === null || now - s.reconnectSentAt >= DEAD_MS)) {
    return { state: { ...s, reconnectSentAt: now }, action: { kind: "reconnect" } };
  }
  return { state: s, action: none };
}

/** "2m 40s", "12s" - the elapsed clock on the working row. */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const sec = total % 60;
  if (m === 0) return `${sec}s`;
  return `${m}m ${sec}s`;
}

/**
 * workingLabel is the one sentence on the working row. `verbFor` turns a
 * tool name into the vocabulary's verb ("Running a command"), so a raw tool
 * id never reaches the screen.
 */
export function workingLabel(s: LivenessState, now: number, verbFor: (tool: string) => string): string {
  const since = Math.max(s.lastFrameAt, s.startedAt);
  if (s.reconnectSentAt !== null || (s.attachSentAt !== null && now - since >= QUIET_MS)) {
    return "Reconnecting…";
  }
  if (s.stopping) return "Stopping…";
  const elapsed = s.inFlight ? ` · ${formatElapsed(now - s.startedAt)}` : "";
  switch (s.phase) {
    case "thinking":
    case "starting":
      return `Thinking${elapsed}`;
    case "streaming":
      return "Responding…";
    case "tool":
      return `${s.toolName ? verbFor(s.toolName) : "Working"}${elapsed}`;
    case "awaiting_approval":
      return "Waiting for your approval";
    case "steering":
      return `Reading your message${elapsed}`;
    case "stopping":
      return "Stopping…";
    default:
      return "Working…";
  }
}
