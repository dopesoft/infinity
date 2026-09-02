import { describe, expect, it } from "vitest";
// Relative: the test runner does not resolve the "@/" alias in this module,
// the same reason activity.ts imports its own value dependencies relatively.
import { deriveStatus } from "./activity";
import type { ChatMessage } from "@/hooks/useChat";

/* The rule this file pins, in the boss's words:
 *
 *   "it looked like 'run a command' was still spinning and time ticking
 *    upwards even tho i had no stop button"
 *
 * No stop button means the turn is over. Nothing coding means the job is over
 * too. A row that goes on counting up in that state is the interface claiming
 * work that nobody is doing, which is the same false-green this codebase
 * refuses everywhere else.
 *
 * deriveStatus is the one place a row's live-ness is decided, so the contract
 * is pinned here: a call with no result reads running, and a call marked
 * interrupted (which is what settling stamps) never does. */

function call(over: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: "m1",
    role: "tool",
    text: "",
    createdAt: Date.now(),
    toolCall: { name: "claude_code__bash", input: { command: "go build ./..." }, nested: true },
    ...over,
  } as ChatMessage;
}

describe("a forwarded step's live-ness", () => {
  it("reads running while it has no result", () => {
    expect(deriveStatus(call()).status).toBe("running");
  });

  it("stops reading running once it has been settled", () => {
    expect(deriveStatus(call({ interrupted: true })).status).toBe("stopped");
  });

  it("reads done when its real result arrives", () => {
    const settled = call({ toolResult: { output: "ok", is_error: false } } as Partial<ChatMessage>);
    expect(deriveStatus(settled).status).toBe("done");
  });
});
