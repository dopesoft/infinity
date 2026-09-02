import { describe, expect, it } from "vitest";
// Relative for the value import: the test runner does not resolve the "@/"
// alias. The type import is erased, so it may keep it.
import { settleNestedOnly } from "./settle";
import type { ChatMessage } from "@/hooks/useChat";

/* This runs MID-TURN, every time the last coding job stops, which routinely
 * happens with the turn still going. So the rule it has to obey is not "close
 * the right rows", it is "touch NOTHING ELSE".
 *
 * The version it replaces reused settleInFlight, which also clears `pending`
 * on the assistant bubble. That is correct at the end of a turn and
 * catastrophic in the middle of one: the bubble the deltas were streaming into
 * stopped being pending, and the reply, already written and saved server-side,
 * never appeared. The boss: "NOTHING IN MY STREAM SAYS ANY OF THAT." */

const at = 1_000;

function msg(over: Partial<ChatMessage>): ChatMessage {
  return { id: "x", role: "tool", text: "", createdAt: at, ...over } as ChatMessage;
}

describe("settling forwarded coding steps mid-turn", () => {
  it("never disturbs the assistant bubble the reply is streaming into", () => {
    const streaming = msg({ id: "a", role: "assistant", text: "Working. Here's where it", pending: true });
    const out = settleNestedOnly(
      [streaming, msg({ id: "n", toolCall: { id: "t1", name: "claude_code__bash", nested: true } })],
      2_000,
    );
    expect(out[0].pending).toBe(true);
    expect(out[0].text).toBe("Working. Here's where it");
  });

  it("closes a forwarded step that never got its result", () => {
    const out = settleNestedOnly([msg({ id: "n", toolCall: { id: "t1", name: "claude_code__bash", nested: true } })], 2_000);
    expect(out[0].interrupted).toBe(true);
    expect(out[0].endedAt).toBe(2_000);
  });

  it("leaves the chat brain's own tool rows alone", () => {
    const own = msg({ id: "o", toolCall: { id: "t2", name: "bash_run" } });
    expect(settleNestedOnly([own], 2_000)[0].interrupted).toBeUndefined();
  });

  it("returns the same array when there is nothing open, so no render is forced", () => {
    const settled = [msg({ id: "n", toolCall: { id: "t1", name: "claude_code__bash", nested: true }, interrupted: true })];
    expect(settleNestedOnly(settled, 2_000)).toBe(settled);
  });
});
