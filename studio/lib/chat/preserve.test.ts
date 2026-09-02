/**
 * A message the server has no copy of must not be deleted by a refetch.
 *
 * The regression this pins (2026-09-01): "a quick error popped in and then
 * disappeared", and separately "it had a message that told me why my pursuit
 * was bare, I left the page, came back and that message was gone". Both are
 * the same rule getting it wrong.
 */

import { describe, expect, it } from "vitest";

import type { ChatMessage } from "@/hooks/useChat";
import { survivesRefetch } from "./preserve";

let seq = 0;
function msg(over: Partial<ChatMessage>): ChatMessage {
  seq++;
  return {
    id: `m-${seq}`,
    role: "assistant",
    text: "",
    createdAt: 1_700_000_000_000 + seq,
    ...over,
  } as ChatMessage;
}

describe("what survives a server refetch", () => {
  it("keeps an error card, which carries no pending flag", () => {
    // This is the exact shape useChat builds from an "error" frame: settled,
    // empty text, the message in `error`. A pending-only rule deletes it.
    expect(survivesRefetch(msg({ error: "Claude stopped without finishing" }))).toBe(true);
  });

  it("keeps the live tail", () => {
    expect(survivesRefetch(msg({ pending: true }))).toBe(true);
    expect(survivesRefetch(msg({ role: "thinking", pending: true }))).toBe(true);
  });

  it("keeps a steer the server has not written down yet", () => {
    expect(survivesRefetch(msg({ role: "user", text: "wait, do it this way", steered: true }))).toBe(
      true,
    );
  });

  it("lets the server own everything it actually holds", () => {
    // A settled assistant reply and a plain user message are both in the
    // transcript. Keeping local copies would show them twice.
    expect(survivesRefetch(msg({ text: "here you go" }))).toBe(false);
    expect(survivesRefetch(msg({ role: "user", text: "do the thing" }))).toBe(false);
    // A pending TOOL card is handled by the caller's id-based de-dupe, not
    // by this rule, or it would be kept twice.
    expect(survivesRefetch(msg({ role: "tool", pending: true }))).toBe(false);
  });
});
