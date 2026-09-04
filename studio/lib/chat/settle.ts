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

/**
 * promoteLastReply - THE LAST THING JARVIS SAID IN A TURN IS HIS REPLY.
 *
 * A message that streams before a tool call is marked interim the moment the
 * call lands ("let me look…"), and interim rows fold into the ledger. That is
 * right while the turn goes on. When the turn ENDS without a later reply (a
 * provider error, a usage cap, Stop), the folded message was the last thing he
 * was told, and folding it hides it behind a one-line "Talked it through" row.
 * 2026-09-04: a 19,000-character research report vanished that way when the
 * ChatGPT plan ran dry four seconds after it streamed.
 *
 * Runs when a turn settles. Finds the turn's last assistant message: by
 * `turnId` when the frames carried one, else everything back to the previous
 * message the boss typed (a steer stays inside its turn). If it is interim and
 * no non-interim reply follows it in the turn, it is un-folded with an
 * EXPLICIT `interim: false`, which the transcript merge reads as "promoted
 * here" so a fetch that beats the server's own promotion cannot re-fold it.
 *
 * Returns the SAME array when there is nothing to do: the `complete` frame can
 * be replayed on a re-attach, and a no-op must not re-render the transcript.
 */
export function promoteLastReply(messages: ChatMessage[], turnId?: string): ChatMessage[] {
  let last = -1;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (turnId) {
      if (m.turnId && m.turnId !== turnId) continue;
    } else if (m.role === "user" && !m.steered) {
      break;
    }
    if (m.role !== "assistant" || m.error) continue;
    if (!m.interim) return messages; // a real reply already follows the narration
    last = i;
    break;
  }
  if (last === -1) return messages;
  const next = messages.slice();
  next[last] = { ...next[last], interim: false };
  return next;
}
