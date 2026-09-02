import { describe, expect, it } from "vitest";

import { toolRowToMessage } from "./toolRow";

// "im left with spinners only": a reload could not tell a command still
// running from one that finished quietly, or from one whose turn died.
describe("a tool card rebuilt from the transcript", () => {
  const base = { tool_call_id: "toolu_1", tool_name: "claude_code__bash" };

  it("is still running when the server says its turn is live", () => {
    const m = toolRowToMessage({ ...base, tool_running: true }, "m1", 1);
    expect(m.pending).toBe(true);
    expect(m.toolResult).toBeUndefined();
    expect(m.interrupted).toBeUndefined();
  });

  it("is stopped when its turn ended without a result", () => {
    const m = toolRowToMessage({ ...base, tool_interrupted: true }, "m1", 1);
    expect(m.interrupted).toBe(true);
    expect(m.pending).toBeUndefined();
    expect(m.toolResult).toBeUndefined();
  });

  it("is done when it finished, even with nothing printed", () => {
    // The server omits an empty tool_output entirely. That used to read as
    // "no result" and spin forever.
    const m = toolRowToMessage({ ...base }, "m1", 1);
    expect(m.toolResult?.output).toBe("");
    expect(m.pending).toBeUndefined();
    expect(m.interrupted).toBeUndefined();
  });

  it("carries a real result through untouched", () => {
    const m = toolRowToMessage({ ...base, tool_output: "ok", tool_is_error: true }, "m1", 1);
    expect(m.toolResult).toMatchObject({ output: "ok", is_error: true });
  });
});
