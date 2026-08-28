import { describe, expect, it } from "vitest";

import { reconcileSteerEcho } from "./steer";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * These tests encode WHY the reconciliation exists, not just what it returns.
 * The boss saw his own message rendered twice — once plain, once "STEERED
 * MID-TURN" — because the client and the server disagreed about whether a
 * send was a message or a steer. Any change that lets a duplicate through
 * must fail here.
 */

const user = (text: string, extra: Partial<ChatMessage> = {}): ChatMessage => ({
  id: `u-${text}-${extra.steered ? "s" : "p"}`,
  role: "user",
  text,
  createdAt: 1,
  ...extra,
});

const assistant = (text: string): ChatMessage => ({
  id: `a-${text}`,
  role: "assistant",
  text,
  createdAt: 1,
});

describe("reconcileSteerEcho", () => {
  it("upgrades the bubble in place when the client drew a plain message the server treated as a steer", () => {
    // THE REPORTED BUG. The client thought no turn was running (the ledger had
    // already collapsed), so it drew a plain bubble; the server auto-routed the
    // send to a steer and echoed it back.
    const prev = [user("why are we stopped??")];
    const next = reconcileSteerEcho(prev, "why are we stopped??", "new-id");

    expect(next).toHaveLength(1); // never two bubbles for one send
    expect(next[0].id).toBe(prev[0].id); // the SAME bubble, not a replacement
    expect(next[0].steered).toBe(true); // and it tells the truth about routing
  });

  it("does nothing when the bubble was already drawn as a steer", () => {
    const prev = [user("carry on", { steered: true })];
    const next = reconcileSteerEcho(prev, "carry on", "new-id");
    expect(next).toBe(prev); // identity: React must be able to skip the render
  });

  it("appends when the echo is a message this tab never drew", () => {
    // Multi-tab parity and reconnect replay: the newest local user message is
    // something else, so this echo is genuinely new and must be shown.
    const prev = [user("earlier thing"), assistant("on it")];
    const next = reconcileSteerEcho(prev, "typed in another tab", "new-id");

    expect(next).toHaveLength(3);
    expect(next[2]).toMatchObject({
      id: "new-id",
      role: "user",
      text: "typed in another tab",
      steered: true,
    });
  });

  it("reconciles an attachment-only send, whose local text is a placeholder", () => {
    const prev = [
      user("(attached: shot.png)", {
        attachments: [{ id: "a1", name: "shot.png" } as never],
      }),
    ];
    const next = reconcileSteerEcho(prev, "(attached: 1 file)", "new-id");

    expect(next).toHaveLength(1);
    expect(next[0].steered).toBe(true);
  });

  it("looks past trailing thinking and tool rows to find the user's bubble", () => {
    // The optimistic user bubble is followed by a thinking placeholder and any
    // number of tool rows, so a naive "check the last message" never matches.
    const prev: ChatMessage[] = [
      user("run the build"),
      { id: "t1", role: "thinking", text: "", pending: true, createdAt: 1 },
      { id: "t2", role: "tool", text: "", createdAt: 1 },
    ];
    const next = reconcileSteerEcho(prev, "run the build", "new-id");

    expect(next).toHaveLength(3); // nothing appended
    expect(next[0].steered).toBe(true);
  });

  it("does not touch an older bubble that happens to share the text", () => {
    // Ask the same thing twice in one session: only the NEWEST send is what
    // this echo can be about, so the older one must stay as it was.
    const prev = [user("status?", { id: "old" }), assistant("ok"), user("status?", { id: "new" })];
    const next = reconcileSteerEcho(prev, "status?", "unused");

    expect(next).toHaveLength(3);
    expect(next.find((m) => m.id === "new")?.steered).toBe(true);
    expect(next.find((m) => m.id === "old")?.steered).toBeUndefined();
  });
});
