/**
 * toolRow.ts - a persisted tool card, rebuilt from the transcript.
 *
 * PURE, and in `lib/chat` rather than inside `useChat`, for the same reason
 * `settle.ts` and `preserve.ts` are: no React, so the rule can be tested in
 * plain node instead of asserted in a comment.
 */

// Type-only: erased at compile time, so this module pulls in no React.
import type { ChatMessage } from "@/hooks/useChat";

/** The fields of a server transcript row that describe a tool call. */
export interface ToolRowFields {
  tool_call_id: string;
  tool_name?: string | null;
  tool_input?: unknown;
  tool_output?: string | null;
  tool_is_error?: boolean;
  tool_running?: boolean;
  tool_interrupted?: boolean;
}

/**
 * toolRowToMessage rebuilds the inline card so it survives navigation and
 * reload in the SAME state it was in live.
 *
 * Three states have to be told apart, and the server now says which:
 *  • still running: its turn is live, so there honestly is no result yet
 *  • stopped: its turn ended and no result was ever filed
 *  • done, which includes a command that printed nothing
 *
 * Before this, "no output" and "no result" were one absence, so an empty
 * result and a dead call both spun forever after a reload (2026-09-02).
 */
export function toolRowToMessage(r: ToolRowFields, id: string, createdAt: number): ChatMessage {
  const msg: ChatMessage = {
    id,
    role: "tool",
    text: "",
    createdAt,
    toolCall: {
      id: r.tool_call_id,
      name: r.tool_name ?? "",
      input: r.tool_input as Record<string, unknown> | undefined,
    },
  };
  if (r.tool_running) {
    msg.pending = true;
  } else if (r.tool_interrupted) {
    msg.interrupted = true;
    msg.endedAt = createdAt;
  } else {
    msg.toolResult = {
      id: r.tool_call_id,
      name: r.tool_name ?? "",
      output: r.tool_output ?? "",
      is_error: r.tool_is_error || undefined,
    };
  }
  return msg;
}
