/**
 * The transcript is merged BY IDENTITY, never by comparing text. Each test
 * here pins a bug the old text-matching merge produced, so a change that
 * brings a text rule back must fail here.
 */

import { describe, expect, it } from "vitest";

import { applyDelta, mergeTranscript, messageKey, rowToMessage, type TranscriptRow } from "./transcript";
import type { ChatMessage } from "@/hooks/useChat";

let n = 0;
const makeId = () => `local-${++n}`;

const row = (extra: Partial<TranscriptRow> & Pick<TranscriptRow, "role" | "text">): TranscriptRow => ({
  id: extra.id ?? `obs-${extra.role}-${extra.text}`,
  created_at: "2026-09-04T05:00:00Z",
  ...extra,
});

const local = (m: Partial<ChatMessage> & Pick<ChatMessage, "role" | "text">): ChatMessage => ({
  id: makeId(),
  createdAt: Date.parse("2026-09-04T05:00:00Z"),
  ...m,
});

describe("messageKey", () => {
  it("names a reply by its turn and index, a user message by its client id, a tool by its call id", () => {
    expect(messageKey(local({ role: "assistant", text: "x", turnId: "t1", msgIndex: 0 }))).toBe("reply:t1:0");
    expect(messageKey(local({ role: "user", text: "x", clientId: "c1" }))).toBe("user:c1");
    expect(messageKey(local({ role: "tool", text: "", toolCall: { id: "tc1", name: "bash_run" } }))).toBe("tool:tc1");
    expect(messageKey(local({ role: "assistant", text: "x", serverId: "obs-9" }))).toBe("row:obs-9");
  });

  it("has no name for a row nothing can identify", () => {
    expect(messageKey(local({ role: "assistant", text: "x" }))).toBeNull();
    expect(messageKey(local({ role: "thinking", text: "" }))).toBeNull();
  });
});

describe("mergeTranscript", () => {
  it("a reply is NOT eaten by an earlier row that happens to start with the same words", () => {
    // The old merge dropped any pending bubble that was a PREFIX of a
    // same-role server row. Two answers starting "Good afternoon, boss." in
    // one chat meant the second one vanished mid-stream.
    const earlier = row({ role: "assistant", text: "Good afternoon, boss. Nothing new in the inbox.", turn_id: "t1", message_index: 0 });
    const streaming = local({ role: "assistant", text: "Good afternoon, boss.", pending: true, turnId: "t2", msgIndex: 0 });
    const out = mergeTranscript([streaming], [earlier], makeId);
    expect(out.map((m) => m.text)).toEqual([earlier.text, "Good afternoon, boss."]);
    expect(out[1].pending).toBe(true);
  });

  it("the live bubble becomes its persisted row, keeping its React id", () => {
    const streaming = local({ role: "assistant", text: "partial", pending: true, turnId: "t1", msgIndex: 0, latencyMs: 1200 });
    const persisted = row({ role: "assistant", text: "partial and the rest, with a caveat", turn_id: "t1", message_index: 0 });
    const out = mergeTranscript([streaming], [persisted], makeId);
    expect(out).toHaveLength(1);
    expect(out[0].id).toBe(streaming.id);
    expect(out[0].text).toBe(persisted.text);
    expect(out[0].pending).toBe(false);
    expect(out[0].latencyMs).toBe(1200);
    expect(out[0].serverId).toBe(persisted.id);
  });

  it("the boss's message is matched by the id the browser minted, never drawn twice", () => {
    const mine = local({ role: "user", text: "fix the tests", clientId: "cm-1", steered: true });
    const theirs = row({ role: "user", text: "fix the tests", client_id: "cm-1", steered: true });
    const out = mergeTranscript([mine], [theirs], makeId);
    expect(out).toHaveLength(1);
    expect(out[0].id).toBe(mine.id);
    expect(out[0].steered).toBe(true);
  });

  it("a named local row the server does not hold yet is kept (hooks persist a moment behind the socket)", () => {
    const reply = local({ role: "assistant", text: "done.", turnId: "t1", msgIndex: 1 });
    const older = row({ role: "assistant", text: "let me look", turn_id: "t1", message_index: 0, interim: true });
    const out = mergeTranscript([reply], [older], makeId);
    expect(out.map((m) => m.text)).toEqual(["let me look", "done."]);
  });

  it("a second local copy of a tool card the server holds is a duplicate, not a keeper", () => {
    const a = local({ role: "tool", text: "", toolCall: { id: "tc1", name: "bash_run" }, pending: true });
    const b = local({ role: "tool", text: "", toolCall: { id: "tc1", name: "bash_run" }, pending: true });
    const done = row({ role: "tool", text: "", tool_call_id: "tc1", tool_name: "bash_run", tool_output: "ok" });
    const out = mergeTranscript([a, b], [done], makeId);
    expect(out).toHaveLength(1);
    expect(out[0].toolResult?.output).toBe("ok");
    expect(out[0].pending).toBeFalsy();
  });

  it("a running local card stays live while the server also says running", () => {
    const live = local({ role: "tool", text: "", toolCall: { id: "tc1", name: "bash_run", input: { cmd: "go test" } }, pending: true });
    const running = row({ role: "tool", text: "", tool_call_id: "tc1", tool_name: "bash_run", tool_running: true });
    const out = mergeTranscript([live], [running], makeId);
    expect(out[0].pending).toBe(true);
    expect(out[0].toolCall?.input).toEqual({ cmd: "go test" });
  });

  it("an unnamed settled local row is replaced by the server (an old cache)", () => {
    const cached = local({ role: "assistant", text: "old cached copy" });
    const truth = row({ role: "assistant", text: "the real row", turn_id: "t1", message_index: 0 });
    const out = mergeTranscript([cached], [truth], makeId);
    expect(out.map((m) => m.text)).toEqual(["the real row"]);
  });

  it("an unnamed live row survives, except when a same-role server row has exactly its text", () => {
    const thinking = local({ role: "thinking", text: "", pending: true });
    const errorCard = local({ role: "assistant", text: "", error: "boom" });
    const dupUser = local({ role: "user", text: "hello", steered: true });
    const rows = [row({ role: "user", text: "hello", steered: true }), row({ role: "assistant", text: "hi" })];
    const out = mergeTranscript([thinking, errorCard, dupUser], rows, makeId);
    expect(out.filter((m) => m.role === "user")).toHaveLength(1);
    expect(out.some((m) => m.role === "thinking")).toBe(true);
    expect(out.some((m) => m.error === "boom")).toBe(true);
  });

  it("a server error row and the local card for the same error are one row", () => {
    const card = local({ role: "assistant", text: "", error: "rate limited" });
    const durable = row({ role: "assistant", text: "rate limited", kind: "error", id: "err:t1", turn_id: "t1" });
    const out = mergeTranscript([card], [durable], makeId);
    expect(out).toHaveLength(1);
    expect(out[0].error).toBe("rate limited");
  });

  it("rows are ordered by time", () => {
    const later = row({ role: "assistant", text: "b", created_at: "2026-09-04T05:00:02Z", turn_id: "t1", message_index: 1 });
    const earlier = row({ role: "user", text: "a", created_at: "2026-09-04T05:00:01Z", client_id: "c1" });
    const out = mergeTranscript([], [later, earlier], makeId);
    expect(out.map((m) => m.text)).toEqual(["a", "b"]);
  });
});

describe("rowToMessage", () => {
  it("carries every identity the server hands back", () => {
    const m = rowToMessage(row({ role: "assistant", text: "x", id: "obs-1", turn_id: "t1", message_index: 2 }), makeId);
    expect(m.serverId).toBe("obs-1");
    expect(m.turnId).toBe("t1");
    expect(m.msgIndex).toBe(2);
    const u = rowToMessage(row({ role: "user", text: "y", client_id: "cm-7" }), makeId);
    expect(u.clientId).toBe("cm-7");
  });
});

describe("applyDelta", () => {
  it("two indices are two bubbles: narration before a tool call and the reply after it stay apart", () => {
    let msgs = applyDelta([], { text: "let me look", turnId: "t1", msgIndex: 0 }, makeId, 1);
    msgs = applyDelta(msgs, { text: "done.", turnId: "t1", msgIndex: 1 }, makeId, 2);
    msgs = applyDelta(msgs, { text: " Two files.", turnId: "t1", msgIndex: 1 }, makeId, 3);
    expect(msgs.map((m) => m.text)).toEqual(["let me look", "done. Two files."]);
  });

  it("finds its bubble past a nested coding step that landed after it", () => {
    const bubble = local({ role: "assistant", text: "Kicking off", pending: true, turnId: "t1", msgIndex: 0 });
    const step = local({ role: "tool", text: "", toolCall: { id: "n1", name: "claude_code__edit", nested: true } });
    const out = applyDelta([bubble, step], { text: " the build.", turnId: "t1", msgIndex: 0 }, makeId, 5);
    expect(out).toHaveLength(2);
    expect(out[0].text).toBe("Kicking off the build.");
  });

  it("a delta for another turn never lands in this turn's bubble", () => {
    const bubble = local({ role: "assistant", text: "old", pending: true, turnId: "t1", msgIndex: 0 });
    const out = applyDelta([bubble], { text: "new", turnId: "t2", msgIndex: 0 }, makeId, 5);
    expect(out.map((m) => m.text)).toEqual(["old", "new"]);
    expect(out[1].turnId).toBe("t2");
  });

  it("without ids (an old core) the tail rule still applies", () => {
    const bubble = local({ role: "assistant", text: "a", pending: true });
    expect(applyDelta([bubble], { text: "b" }, makeId, 1)[0].text).toBe("ab");
    const settled = local({ role: "assistant", text: "a" });
    expect(applyDelta([settled], { text: "b" }, makeId, 1)).toHaveLength(2);
  });
});
