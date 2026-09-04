/**
 * The last thing Jarvis said in a turn is his reply, never narration.
 *
 * 2026-09-04: a 19,000-character research report streamed, a tool call
 * followed it (so it was marked interim and folded into the ledger), and the
 * ChatGPT plan ran dry before a later reply. The report sat behind a one-line
 * "Talked it through 2 times" row. Every case here is a way that must not
 * happen again, or a way the rule must NOT fire.
 */

import { describe, expect, it } from "vitest";

import { promoteLastReply } from "./settle";
import { mergeTranscript } from "./transcript";
import type { ChatMessage } from "@/hooks/useChat";

let n = 0;
const msg = (m: Partial<ChatMessage> & Pick<ChatMessage, "role" | "text">): ChatMessage => ({
  id: `m-${++n}`,
  createdAt: n,
  ...m,
});

describe("promoteLastReply", () => {
  it("un-folds the last message of a turn that ended without a later reply", () => {
    const rows = [
      msg({ role: "user", text: "research it" }),
      msg({ role: "assistant", text: "let me look", interim: true, turnId: "t1", msgIndex: 0 }),
      msg({ role: "tool", text: "", toolCall: { id: "c1", name: "web_search" }, turnId: "t1" }),
      msg({ role: "assistant", text: "Boss, here is the honest 2026 read.", interim: true, turnId: "t1", msgIndex: 1 }),
      msg({ role: "tool", text: "", toolCall: { id: "c2", name: "tool_search" }, turnId: "t1" }),
    ];
    const out = promoteLastReply(rows, "t1");
    expect(out[3].interim).toBe(false);
    expect(out[1].interim).toBe(true);
  });

  it("leaves narration folded when a real reply follows it", () => {
    const rows = [
      msg({ role: "assistant", text: "let me look", interim: true, turnId: "t1", msgIndex: 0 }),
      msg({ role: "assistant", text: "Done.", turnId: "t1", msgIndex: 1 }),
    ];
    expect(promoteLastReply(rows, "t1")).toBe(rows);
  });

  it("is a no-op the second time (a replayed complete frame)", () => {
    const rows = [msg({ role: "assistant", text: "x", interim: true, turnId: "t1", msgIndex: 0 })];
    const once = promoteLastReply(rows, "t1");
    expect(once).not.toBe(rows);
    expect(promoteLastReply(once, "t1")).toBe(once);
  });

  it("a nested coding step or an error card after the reply does not hide it", () => {
    const rows = [
      msg({ role: "assistant", text: "the reply", interim: true, turnId: "t1", msgIndex: 0 }),
      msg({ role: "tool", text: "", toolCall: { id: "n1", name: "claude_code__edit", nested: true } }),
      msg({ role: "assistant", text: "", error: "usage limit", turnId: "t1" }),
    ];
    expect(promoteLastReply(rows, "t1")[0].interim).toBe(false);
  });

  it("without a turn id it stops at the message he typed, and a steer stays inside the turn", () => {
    const rows = [
      msg({ role: "assistant", text: "previous turn's narration", interim: true }),
      msg({ role: "user", text: "go" }),
      msg({ role: "assistant", text: "on it", interim: true }),
      msg({ role: "user", text: "i approve", steered: true }),
      msg({ role: "assistant", text: "the report", interim: true }),
    ];
    const out = promoteLastReply(rows);
    expect(out[4].interim).toBe(false);
    expect(out[2].interim).toBe(true);
    expect(out[0].interim).toBe(true);
  });

  it("never touches another turn's rows", () => {
    const rows = [msg({ role: "assistant", text: "other", interim: true, turnId: "t0", msgIndex: 0 })];
    expect(promoteLastReply(rows, "t1")).toBe(rows);
  });
});

describe("a promoted reply survives the transcript merge", () => {
  it("keeps interim cleared when the server row still says interim (its promotion lags a fetch)", () => {
    const local = promoteLastReply([msg({ role: "assistant", text: "the report", interim: true, turnId: "t1", msgIndex: 1 })], "t1");
    const out = mergeTranscript(
      local,
      [{ id: "obs-1", role: "assistant", text: "the report", created_at: "2026-09-04T07:28:03Z", turn_id: "t1", message_index: 1, interim: true }],
      () => "x",
    );
    expect(out).toHaveLength(1);
    expect(out[0].interim).toBeUndefined();
  });

  it("takes the server's word when this browser never promoted the row", () => {
    const local = [msg({ role: "assistant", text: "the report", interim: true, turnId: "t1", msgIndex: 1 })];
    const out = mergeTranscript(
      local,
      [{ id: "obs-1", role: "assistant", text: "the report", created_at: "2026-09-04T07:28:03Z", turn_id: "t1", message_index: 1 }],
      () => "x",
    );
    expect(out[0].interim).toBeUndefined();
    const folded = mergeTranscript(
      local,
      [{ id: "obs-1", role: "assistant", text: "the report", created_at: "2026-09-04T07:28:03Z", turn_id: "t1", message_index: 1, interim: true }],
      () => "x",
    );
    expect(folded[0].interim).toBe(true);
  });

  it("a cut reply keeps its interrupted hint on reload", () => {
    const out = mergeTranscript(
      [],
      [{ id: "obs-1", role: "assistant", text: "half", created_at: "2026-09-04T07:28:03Z", turn_id: "t1", message_index: 0, interrupted: true }],
      () => "x",
    );
    expect(out[0].interrupted).toBe(true);
  });
});
