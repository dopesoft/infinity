/**
 * settle.ts - closing rows that nobody is going to close.
 *
 * PURE, and in `lib/chat` rather than inside `useChat`, for the same reason
 * `activity.ts` is: no React, no timers, so the rule can be tested in plain
 * node instead of being asserted in a comment.
 */

// Type-only: erased at compile time, so this module pulls in no React.
import type { ChatMessage } from "@/hooks/useChat";

/**
 * settleNestedOnly closes forwarded coding steps and NOTHING else.
 *
 * It runs MID-TURN - whenever the last coding job stops, which routinely
 * happens while the turn is still going - so anything else it touched would be
 * collateral damage on live state.
 *
 * The version it replaces reused `settleInFlight`, which also clears `pending`
 * on the assistant bubble. That is correct at the END of a turn and
 * catastrophic in the MIDDLE of one: the bubble the reply was streaming into
 * stopped being pending, so the answer - already written, already saved
 * server-side - never appeared on screen. The boss saw a red row and silence,
 * and only found his reply by reloading the page. "THATS WHY I DIDNT SEE A
 * RESPONSE CUZ I HAVE TO HIT REFRESH TO FUCKIN SEE IT!"
 *
 * Returns the SAME array when there is nothing open, so a no-op cannot force a
 * re-render of the transcript.
 */
export function settleNestedOnly(messages: ChatMessage[], now: number): ChatMessage[] {
  const open = (m: ChatMessage) => !!m.toolCall?.nested && !m.toolResult && !m.interrupted;
  if (!messages.some(open)) return messages;
  return messages.map((m) => (open(m) ? { ...m, interrupted: true, endedAt: now } : m));
}
