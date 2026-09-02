/**
 * preserve.ts - which locally-rendered messages survive a server refetch.
 *
 * PURE, and in `lib/chat` rather than inside `useChat`, for the same reason
 * `settle.ts` is: no React, no timers, so the rule can be tested in plain node
 * instead of being asserted in a comment.
 *
 * Two paths refetch the transcript and both used to answer this question
 * differently: the reconcile merge kept local tool cards and the pending tail,
 * while the mount fetch kept only orphaned steered messages and replaced
 * everything else. One predicate now, used by both, so they cannot drift.
 */

// Type-only: erased at compile time, so this module pulls in no React.
import type { ChatMessage } from "@/hooks/useChat";

/**
 * survivesRefetch reports whether a local message must be kept when the
 * canonical transcript comes back from Core.
 *
 * The server transcript is authoritative for everything it HOLDS. These three
 * are the cases where it holds nothing, so local is the only copy:
 *
 *  • **A live tail.** A pending bubble or a thinking row is mid-flight by
 *    definition and cannot have been persisted yet.
 *  • **An error.** The card is built settled, with no `pending` flag, so a
 *    tail-only rule deleted it on the next refetch - the boss saw a failure
 *    flash up and remove itself (2026-09-01). Worse than never showing it,
 *    because now he cannot tell whether it happened. And some errors never
 *    reach the transcript at all: the loop can emit one mid-stream and then
 *    recover, and only a turn that CLOSES errored leaves a durable row.
 *  • **A steer he typed.** The hook pipeline is async, so there is a window
 *    between sending and the row landing where the server legitimately does
 *    not have it yet.
 *
 * Tool cards are handled separately by the caller: they are kept AND de-duped
 * against the server's reconstructed copies by tool_call_id, which is a
 * different rule from "keep it because nothing else has it".
 */
export function survivesRefetch(m: ChatMessage): boolean {
  if (m.error) return true;
  if (m.role === "thinking") return true;
  if (m.pending && m.role !== "tool") return true;
  return m.role === "user" && !!m.steered;
}
