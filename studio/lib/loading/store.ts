/**
 * The app-loading store — one counter, read by one thing (the app mark).
 *
 * WHY A STORE AND NOT REACT STATE
 *
 * The thing that STARTS a screen load (a click on a rail icon, a
 * `router.push` from a card) and the thing that SHOWS it (the mark at the
 * top of the rail, or in the mobile bar) are on opposite ends of the tree
 * and neither owns the other. A context provider would work but would
 * re-render the whole app on every begin/end; this is a plain external
 * store read through `useSyncExternalStore`, so only the mark re-renders.
 *
 * KEYS, NOT A COUNT. Callers pass a stable key ("route", "page:<id>") so a
 * double `begin` from the same source can't leave the count stuck above
 * zero, and a component that unmounts mid-load releases exactly its own
 * hold. Two screens loading at once is one spinner, not two.
 *
 * TWO TIMERS, BOTH LOAD-BEARING:
 *
 *  - MIN_VISIBLE_MS. The mark appears the instant a key is taken and stays
 *    for at least one full rotation even if the load finishes in 12ms. The
 *    whole point is the boss knowing his tap registered, and a glyph that
 *    appears and vanishes before it can turn reads as a flicker rather than
 *    as something working.
 *
 *  - The per-key TTL. Any hold auto-releases, so a caller that begins and
 *    never ends (a click on a link something else cancelled, a fetch that
 *    hangs) cannot spin the logo forever. A stuck spinner is the UI version
 *    of a false green: it says "still working" when nothing is.
 */

/**
 * How long the mark stays up once it is up. Sized to ONE FULL TURN of the
 * pinwheel (750ms, see tailwind.config) plus a little. A shorter hold is what
 * produced "why does it just flash, it doesn't even spin" — a third of a
 * rotation is a flicker, not movement, and movement is the entire signal.
 */
export const MIN_VISIBLE_MS = 800;

type Listener = () => void;

const held = new Map<string, ReturnType<typeof setTimeout> | null>();
const listeners = new Set<Listener>();

let visible = false;
let shownAt = 0;
let hideTimer: ReturnType<typeof setTimeout> | null = null;

function emit(): void {
  listeners.forEach((l) => l());
}

function settle(): void {
  if (held.size > 0) {
    if (hideTimer) {
      clearTimeout(hideTimer);
      hideTimer = null;
    }
    if (!visible) {
      visible = true;
      shownAt = Date.now();
      emit();
    }
    return;
  }
  if (!visible || hideTimer) return;
  const wait = Math.max(0, MIN_VISIBLE_MS - (Date.now() - shownAt));
  hideTimer = setTimeout(() => {
    hideTimer = null;
    if (held.size > 0) return;
    visible = false;
    emit();
  }, wait);
}

/** Take a hold. Idempotent per key; auto-releases after `ttlMs`. */
export function beginLoading(key: string, ttlMs = 12_000): void {
  const existing = held.get(key);
  if (existing) clearTimeout(existing);
  held.set(
    key,
    setTimeout(() => endLoading(key), ttlMs),
  );
  settle();
}

/** Release a hold. Safe to call for a key that was never taken. */
export function endLoading(key: string): void {
  const timer = held.get(key);
  if (timer === undefined) return;
  if (timer) clearTimeout(timer);
  held.delete(key);
  settle();
}

/**
 * Where the app is GOING, while it is going there — the path of the
 * navigation currently in flight, or null when nothing is.
 *
 * It shares this module's listener set rather than owning a second one,
 * because it changes on exactly the same two edges the hold does, and two
 * stores would mean two re-renders for one event.
 *
 * The nav uses it to move its own marker the instant you press, instead of
 * waiting for the new screen to arrive to tell you which one you picked.
 */
let navTarget: string | null = null;

export function setNavTarget(path: string | null): void {
  if (navTarget === path) return;
  navTarget = path;
  emit();
}

export function navTargetSnapshot(): string | null {
  return navTarget;
}

export function navTargetServerSnapshot(): null {
  return null;
}

export function subscribeLoading(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function loadingSnapshot(): boolean {
  return visible;
}

/** The server never has a hold, and hydration must agree with that. */
export function loadingServerSnapshot(): boolean {
  return false;
}
