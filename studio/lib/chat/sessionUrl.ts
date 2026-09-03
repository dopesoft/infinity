/**
 * sessionUrl - keeping the address bar honest about which conversation is open.
 *
 * THE BUG THIS EXISTS FOR. Two places remember which chat you are in: the
 * `?session=` param (how the dashboard, the record sheet and the pursuits open
 * a specific conversation) and localStorage (how a refresh restores the last
 * one). On mount the URL wins. But switching chats in the drawer, and starting
 * a new one, only moved React state and localStorage - the param sat there
 * pointing at whatever conversation you arrived from. So a refresh handed that
 * stale id straight back and landed him in an older chat.
 *
 * Two sources of truth for one fact, and they were allowed to disagree. The
 * fix is that every session change moves BOTH, through one function.
 */

/** The page that owns a conversation. Nothing else gets a session param. */
const LIVE_PATH = "/live";

/**
 * nextSessionHref returns the URL `href` should become so its `session` param
 * names `id`, or null when it already does (or when this isn't the chat page,
 * so an unrelated screen never grows a session param it doesn't own).
 *
 * Every other param is preserved: `?voice=1` and friends survive the rewrite.
 */
export function nextSessionHref(href: string, id: string): string | null {
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return null;
  }
  if (url.pathname !== LIVE_PATH && !url.pathname.startsWith(LIVE_PATH + "/")) return null;
  const current = url.searchParams.get("session")?.trim() ?? "";
  const next = id.trim();
  if (current === next) return null;
  if (next) url.searchParams.set("session", next);
  else url.searchParams.delete("session");
  return url.toString();
}

/**
 * isRestoredSession answers "was I put back where I was, or asked for a
 * specific conversation?" - the difference that decides whether the stale-chat
 * rotation is allowed to fire.
 *
 * It used to be "there is no `?session=` param at all", which stopped being a
 * thing that can happen the moment the address bar started following the open
 * chat. A refresh now carries a param, and what marks it as a restore rather
 * than a deep link is that it agrees with the id that was stored.
 */
export function isRestoredSession(requested: string, stored: string): boolean {
  const req = requested.trim();
  if (!req) return true;
  return req === stored.trim();
}
