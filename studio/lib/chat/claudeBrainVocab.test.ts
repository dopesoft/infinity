/**
 * The Claude Max brain speaks Studio's tool vocabulary.
 *
 * Claude Code names its own tools bare: "Edit", "Write", "Bash", "Read". Every
 * word, glyph and behaviour on this side is keyed on the `claude_code__*`
 * names the MCP bridge produces for the SAME tools, so Core normalises them at
 * the stream boundary (nestedToolName, core/internal/tools/code_agent_steps.go).
 *
 * This pins the receiving half. If a name arrives unmapped it falls through to
 * a generic "Working" row with no file and no diff, which is precisely what
 * "I never see it editing files like other models" looks like on screen. The
 * boss asked that question on 2026-09-01 and it deserves an answer a test can
 * keep giving.
 */

import { describe, expect, it } from "vitest";

import type { ChatMessage } from "@/hooks/useChat";
import { coalesce } from "./activity";
import { isCodeChangeTool, isCodeReadTool, extractToolFilePath } from "../canvas/detection";

let seq = 0;
const T0 = 1_700_000_000_000;

function toolStep(name: string, input: Record<string, unknown>): ChatMessage {
  seq++;
  const id = `call-${seq}`;
  return {
    id: `msg-${seq}`,
    role: "tool",
    text: "",
    createdAt: T0 + seq * 1000,
    toolCall: { id, name, input },
    toolResult: {
      id,
      name,
      output: "ok",
      ended_at: new Date(T0 + seq * 1000 + 400).toISOString(),
    },
  };
}

describe("the names Core sends for a Claude Code turn", () => {
  it("says what it did to the file, never a generic 'Working'", () => {
    const items = coalesce([
      toolStep("claude_code__edit", {
        file_path: "/Users/n0m4d/Dev/infinity/core/internal/pursuits/jh/cockpit.go",
        old_string: "before",
        new_string: "after",
      }),
      toolStep("claude_code__write", {
        file_path: "/Users/n0m4d/Dev/infinity/core/internal/server/pursuits_jh_api.go",
        content: "package server",
      }),
      toolStep("claude_code__bash", { command: "go build ./..." }),
      toolStep("claude_code__read", { file_path: "/tmp/notes.md" }),
    ]);

    const labels = items.map((i) => i.label);
    for (const label of labels) {
      expect(label).not.toMatch(/^Work(ed|ing)$/);
    }
    expect(labels[0]).toMatch(/Edit/i);
    expect(labels[1]).toMatch(/(Wrote|Writing)/i);
    expect(labels[2]).toMatch(/(Ran|Running)/i);
    expect(labels[3]).toMatch(/(Read|Reading)/i);
  });

  it("counts an edit as a code change, so the diff and the Changes column light up", () => {
    expect(isCodeChangeTool("claude_code__edit")).toBe(true);
    expect(isCodeChangeTool("claude_code__write")).toBe(true);
    expect(isCodeChangeTool("claude_code__multiedit")).toBe(true);
    expect(isCodeReadTool("claude_code__read")).toBe(true);
    // A bare name is what arrives if Core ever stops normalising. It must not
    // quietly pass as something else.
    expect(isCodeChangeTool("Edit")).toBe(false);
  });

  it("finds the file Claude names, which it calls file_path", () => {
    expect(extractToolFilePath({ file_path: "core/internal/pursuits/jh/cockpit.go" })).toBe(
      "core/internal/pursuits/jh/cockpit.go",
    );
  });
});
