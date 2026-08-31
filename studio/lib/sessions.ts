/**
 * Session display naming - one answer for "what is this conversation called?".
 *
 * A session is named by the namer at the end of its first turn, so between
 * "New" and the first reply there is nothing to show. That gap used to render
 * a hex slug of the id ("1241-a009"), which reads as a machine reference in
 * the one place the boss looks to know where he is. It is a conversation that
 * has not been named yet, so it says so.
 *
 * Lives here rather than in either consumer because the header and the
 * sessions drawer must agree: the same session cannot be "New Conversation"
 * on the row and "4bbf1a2c…" in the list.
 */

/** Shown until the namer writes a real title. */
export const UNNAMED_SESSION_LABEL = "New Conversation";

export function sessionDisplayName(s: {
  title?: string | null;
  name?: string | null;
  id?: string | null;
}): string {
  return s.title?.trim() || s.name?.trim() || UNNAMED_SESSION_LABEL;
}
