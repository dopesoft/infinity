import { describe, expect, it } from "vitest";
import type { WSEvent } from "@/lib/ws/client";
import {
  DEAD_MS,
  LEGACY_POLL_MS,
  QUIET_MS,
  initialLiveness,
  onConnected,
  onFrame,
  onSend,
  onStop,
  onTick,
  workingLabel,
  type LivenessState,
} from "./liveness";

/**
 * The rules the "did my tap do anything / is he still there" question rests
 * on. Every test is one way the old watchdog got it wrong.
 */

const sid = "s1";
const verb = (tool: string) => (tool === "bash_run" ? "Running a command" : "Doing something");

function heartbeat(seq: number, phase = "thinking", extra: Partial<NonNullable<WSEvent & { type: "heartbeat" }>["turn_status"]> = {}): WSEvent {
  return {
    type: "heartbeat",
    session_id: sid,
    turn_status: { turn_id: "t1", in_flight: true, seq, oldest_seq: 1, phase, elapsed_ms: 0, replayed: 0, ...extra },
  };
}

function turnStatus(inFlight: boolean, seq: number, extra: Record<string, unknown> = {}): WSEvent {
  return {
    type: "turn_status",
    session_id: sid,
    turn_status: { turn_id: "t1", in_flight: inFlight, seq, oldest_seq: 1, phase: inFlight ? "thinking" : undefined, elapsed_ms: 0, replayed: 0, ...extra },
  } as WSEvent;
}

function streaming(now: number): LivenessState {
  return onSend(initialLiveness(), now);
}

describe("liveness", () => {
  it("a 2m40s reasoning gap with heartbeats never attaches, reconnects, or errors", () => {
    let s = streaming(0);
    let t = 0;
    for (; t <= 160_000; t += 5_000) {
      const r = onFrame(s, heartbeat(3), t);
      s = r.state;
      expect(r.action.kind).toBe("none");
      const tick = onTick(s, t + 1_000);
      s = tick.state;
      expect(tick.action.kind).toBe("none");
    }
    expect(s.inFlight).toBe(true);
    expect(workingLabel(s, 160_000, verb)).toBe("Thinking · 2m 40s");
  });

  it("20s of true silence asks once for a replay; 45s rebuilds the socket; never an error", () => {
    let s = streaming(0);
    s = onFrame(s, { type: "delta", session_id: sid, text: "a", seq: 7, turn_id: "t1" }, 1_000).state;
    let r = onTick(s, 1_000 + QUIET_MS - 1);
    expect(r.action.kind).toBe("none");
    r = onTick(r.state, 1_000 + QUIET_MS);
    expect(r.action).toEqual({ kind: "attach", sinceSeq: 7 });
    // Asking again every second would be a storm.
    r = onTick(r.state, 1_000 + QUIET_MS + 1_000);
    expect(r.action.kind).toBe("none");
    r = onTick(r.state, 1_000 + DEAD_MS);
    expect(r.action.kind).toBe("reconnect");
    r = onTick(r.state, 1_000 + DEAD_MS + 1_000);
    expect(r.action.kind).toBe("none");
    // Still streaming from our point of view: the server has not said otherwise.
    expect(r.state.inFlight).toBe(true);
    expect(workingLabel(r.state, 1_000 + DEAD_MS + 1_000, verb)).toBe("Reconnecting…");
  });

  it("a frame after the silence clears the escalation so the next silence starts over", () => {
    let s = streaming(0);
    s = onTick(s, QUIET_MS).state;
    expect(s.attachSentAt).not.toBeNull();
    s = onFrame(s, heartbeat(1), QUIET_MS + 500).state;
    expect(s.attachSentAt).toBeNull();
    expect(onTick(s, QUIET_MS + 1_000).action.kind).toBe("none");
  });

  it("a seq gap asks for a replay from the last seen seq", () => {
    let s = streaming(0);
    s = onFrame(s, { type: "delta", session_id: sid, text: "a", seq: 4, turn_id: "t1" }, 10).state;
    const r = onFrame(s, { type: "delta", session_id: sid, text: "c", seq: 6, turn_id: "t1" }, 20);
    expect(r.action).toEqual({ kind: "attach", sinceSeq: 4 });
    expect(r.duplicate).toBe(false);
    // A heartbeat ahead of us means the same thing.
    const h = onFrame(streaming(0), heartbeat(9), 30);
    expect(h.action.kind).toBe("none"); // nothing seen yet: nothing to be behind on
    const behind = onFrame({ ...streaming(0), lastSeq: 5 }, heartbeat(9), 30);
    expect(behind.action).toEqual({ kind: "attach", sinceSeq: 5 });
  });

  it("a replayed frame at or below the last seq is a duplicate, not new content", () => {
    let s = streaming(0);
    s = onFrame(s, { type: "delta", session_id: sid, text: "a", seq: 4, turn_id: "t1" }, 10).state;
    const r = onFrame(s, { type: "delta", session_id: sid, text: "a", seq: 4, turn_id: "t1", replay: true }, 20);
    expect(r.duplicate).toBe(true);
    expect(r.state.lastSeq).toBe(4);
  });

  it("only the server ends a turn: turn_status in_flight:false settles, deltas and proactive frames do not", () => {
    let s = streaming(0);
    s = onFrame(s, { type: "proactive_message", session_id: sid, text: "psst" }, 5).state;
    expect(s.inFlight).toBe(true);
    const r = onFrame(s, turnStatus(false, 9, { stop_reason: "end_turn" }), 10);
    expect(r.state.inFlight).toBe(false);
    expect(r.action).toEqual({ kind: "reconcile", stopReason: "end_turn" });
    // And an idle status when we were idle is nothing at all.
    const idle = onFrame(initialLiveness(), turnStatus(false, 9), 11);
    expect(idle.action.kind).toBe("none");
  });

  it("turn_status in_flight:true adopts the server's start time and phase", () => {
    const started = new Date(1_000).toISOString();
    const r = onFrame(initialLiveness(), turnStatus(true, 3, { started_at: started, phase: "tool", tool_name: "bash_run" }), 50_000);
    expect(r.state.inFlight).toBe(true);
    expect(r.state.startedAt).toBe(1_000);
    expect(workingLabel(r.state, 13_000, verb)).toBe("Running a command · 12s");
  });

  it("a replay floor past what we have sends us to the transcript", () => {
    const s = { ...streaming(0), lastSeq: 2 };
    const r = onFrame(s, turnStatus(true, 900, { oldest_seq: 500 }), 10);
    expect(r.action.kind).toBe("reconcile");
  });

  it("an old core refusing attach flips to legacy polling and paints nothing", () => {
    let s = streaming(0);
    const r = onFrame(s, { type: "error", session_id: sid, message: "unknown type: attach" }, 10);
    expect(r.duplicate).toBe(true); // never rendered
    expect(r.state.attachSupported).toBe(false);
    expect(r.state.inFlight).toBe(true);
    s = r.state;
    expect(onTick(s, 10 + LEGACY_POLL_MS - 1).action.kind).toBe("none");
    const poll = onTick(s, 10 + LEGACY_POLL_MS);
    expect(poll.action.kind).toBe("reconcile");
    expect(onTick(poll.state, 10 + LEGACY_POLL_MS + 1_000).action.kind).toBe("none");
  });

  it("stop shows Stopping… until the server confirms", () => {
    let s = streaming(0);
    s = onStop(s);
    expect(workingLabel(s, 1_000, verb)).toBe("Stopping…");
    // The heartbeat keeps saying stopping while the loop unwinds...
    s = onFrame(s, heartbeat(2, "stopping"), 2_000).state;
    expect(workingLabel(s, 2_000, verb)).toBe("Stopping…");
    // ...and complete{interrupted} ends it.
    s = onFrame(s, { type: "complete", session_id: sid, stop_reason: "interrupted", seq: 3, turn_id: "t1" }, 3_000).state;
    expect(s.inFlight).toBe(false);
    expect(s.stopping).toBe(false);
  });

  it("reconnecting asks for everything since the last seq", () => {
    let s = streaming(0);
    s = onFrame(s, { type: "delta", session_id: sid, text: "a", seq: 12, turn_id: "t1" }, 10).state;
    expect(onConnected(s)).toEqual({ kind: "attach", sinceSeq: 12 });
    expect(onConnected(initialLiveness())).toEqual({ kind: "attach", sinceSeq: 0 });
  });

  it("labels name the phase and the tool by its verb, never its id", () => {
    let s = streaming(0);
    expect(workingLabel(s, 3_000, verb)).toBe("Thinking · 3s");
    s = onFrame(s, { type: "tool_call", session_id: sid, tool_call: { id: "c1", name: "bash_run" }, seq: 1, turn_id: "t1" }, 100).state;
    expect(workingLabel(s, 12_000, verb)).toBe("Running a command · 12s");
    s = onFrame(s, { type: "tool_call", session_id: sid, tool_call: { id: "c2", name: "fs_write", awaiting_approval: true }, seq: 2, turn_id: "t1" }, 200).state;
    expect(workingLabel(s, 12_000, verb)).toBe("Waiting for your approval");
    s = onFrame(s, { type: "delta", session_id: sid, text: "so", seq: 3, turn_id: "t1" }, 300).state;
    expect(workingLabel(s, 12_000, verb)).toBe("Responding…");
    // A nested step (a coding job reporting from inside its run) is not this turn's phase.
    const before = s.phase;
    s = onFrame(s, { type: "tool_call", session_id: sid, tool_call: { id: "n1", name: "claude_code__bash", nested: true } }, 400).state;
    expect(s.phase).toBe(before);
  });
});
