import { describe, expect, it } from "vitest";
import { coalesce, formatCount } from "./activity";
import type { ChatMessage } from "@/hooks/useChat";

/* Claude Code redacts its reasoning: it sends the thinking block and deltas
 * with an EMPTY string, and reports progress as a running token count instead.
 * So a row that only knows how to quote text showed "Thinking" over an empty
 * box for two minutes, which is indistinguishable from a hang. The boss read it
 * as one, repeatedly: "so i just sit there for minutes with no response".
 *
 * The count is the one real signal available, and it moves. */

function thinking(over: Partial<ChatMessage>): ChatMessage {
  return { id: "t", role: "thinking", text: "", pending: true, createdAt: 0, ...over } as ChatMessage;
}

describe("a thinking row with no words", () => {
  it("shows how much it has reasoned so far", () => {
    const [item] = coalesce([thinking({ thinkingTokens: 1450 })]);
    expect(item.meta).toBe("1.5k tokens of reasoning so far");
    expect(item.status).toBe("running");
  });

  it("prefers the real reasoning when the brain actually sends it", () => {
    const [item] = coalesce([thinking({ text: "Checking the migration order", thinkingTokens: 900 })]);
    expect(item.meta).toBe("Checking the migration order");
  });

  it("says nothing rather than showing a zero", () => {
    const [item] = coalesce([thinking({ thinkingTokens: 0 })]);
    expect(item.meta).toBe("");
  });
});

describe("formatCount", () => {
  it("reads the way a person says it", () => {
    expect(formatCount(950)).toBe("950");
    expect(formatCount(1000)).toBe("1k");
    expect(formatCount(1450)).toBe("1.5k");
    expect(formatCount(12300)).toBe("12.3k");
  });
});
