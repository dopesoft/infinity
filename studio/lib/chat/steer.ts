import type { ChatMessage } from "@/hooks/useChat";

/**
 * reconcileSteerEcho — fold a `steer_received` echo into the transcript
 * without ever drawing the boss's message twice.
 *
 * THE BUG THIS EXISTS TO KILL (reported 2026-08-28: "when i ask jarvis a
 * question, he gets it twice, once as a real message and once as a steered
 * message"):
 *
 * Whether a typed message is a MESSAGE or a STEER is decided in two places
 * that can legitimately disagree:
 *
 *   - The client decides optimistically from `isStreamingRef`, so it can draw
 *     the bubble immediately. If no turn looks to be in flight it draws a
 *     plain bubble (`steered` unset).
 *   - The server decides authoritatively. It "tolerates a stale choice by
 *     auto-routing message→steer and steer→message", then echoes
 *     `steer_received`.
 *
 * The window between those two is real: a turn whose steps have settled (the
 * ledger has already collapsed to "Worked for 6m 28s") but whose server-side
 * loop has not finished is EXACTLY when the boss types, because it looks done.
 *
 * The old dedupe dropped the echo only when the newest user bubble was
 * ALREADY marked steered — the one case that cannot happen in this race. So
 * the disagreement always produced a duplicate.
 *
 * The fix reconciles on the thing both sides agree about — the text — and
 * treats the server as the authority on the flag: the bubble already on
 * screen is UPGRADED to steered rather than a second one being appended.
 * Matching by identity like this is what makes it a mechanic rather than a
 * hopeful heuristic, per CLAUDE.md Rule #1b.
 *
 * Returns `prev` unchanged when there is nothing to do, so React can skip the
 * re-render.
 *
 * @param prev      current transcript
 * @param text      the echoed text as the server saw it
 * @param newId     id to use if the echo is genuinely new (another tab, a
 *                  reconnect replay) and a bubble must be appended
 * @param now       clock, for the appended bubble
 * @param clientId  the browser's own id for the message, when the server
 *                  echoed one: the exact match, tried before any text rule
 */
export function reconcileSteerEcho(
  prev: ChatMessage[],
  text: string,
  newId: string,
  now: number = Date.now(),
  clientId?: string,
): ChatMessage[] {
  // By identity first. A message this browser sent carries the id it minted;
  // the server hands it back on the echo, so no text has to be compared.
  if (clientId) {
    for (let i = prev.length - 1; i >= 0; i--) {
      const m = prev[i];
      if (m.role !== "user" || m.clientId !== clientId) continue;
      if (m.steered) return prev;
      const next = prev.slice();
      next[i] = { ...m, steered: true };
      return next;
    }
  }
  for (let i = prev.length - 1; i >= 0; i--) {
    const m = prev[i];
    if (m.role !== "user") continue;

    // An attachment-only send renders as "(attached: …)" locally while the
    // server echoes its own marker, so those reconcile on having attachments
    // rather than on an exact string match.
    const sameText =
      m.text === text ||
      (text.startsWith("(attached:") && !!m.attachments?.length);

    // The newest user bubble is something else entirely — this echo really is
    // a message we have never drawn (another tab steered, or a reconnect is
    // replaying). Fall through and append it.
    if (!sameText) break;

    // Already drawn as a steer: the common single-tab case, nothing to do.
    if (m.steered) return prev;

    // The disagreement case. We drew it as a plain message; the server says
    // it was a steer. Upgrade in place — this both removes the duplicate and
    // makes the transcript honest, because it WAS delivered mid-turn.
    const next = prev.slice();
    next[i] = { ...m, steered: true };
    return next;
  }

  return [
    ...prev,
    { id: newId, role: "user", text, steered: true, createdAt: now, clientId },
  ];
}
