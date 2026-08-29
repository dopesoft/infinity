/**
 * Pure helpers behind <ScopedTabs>.
 *
 * The rule they encode: a search field belongs to whatever sits directly
 * above it. Tabs above means it searches THAT TAB. The dangerous version of
 * that rule is a scoped search that can silently hide a match in another tab
 * — you search once, get nothing, and conclude the thing does not exist.
 * `elsewhere()` is what closes that trap.
 */

export type TabCounts = Record<string, number>;

export type Elsewhere = {
  /** Tabs other than the active one that hold at least one match. */
  hits: { id: string; label: string; count: number }[];
  total: number;
};

/**
 * Given the active tab and per-tab match counts, work out what to say when
 * the active tab is empty.
 *
 * Returns null when there is nothing to say — either the active tab has
 * matches (so no empty state at all) or nothing anywhere does (a plain "no
 * matches" is the honest answer, and pointing at zero other tabs would be
 * noise).
 */
export function elsewhere(
  activeId: string,
  tabs: { id: string; label: string }[],
  counts: TabCounts,
): Elsewhere | null {
  if ((counts[activeId] ?? 0) > 0) return null;
  const hits = tabs
    .filter((t) => t.id !== activeId && (counts[t.id] ?? 0) > 0)
    .map((t) => ({ id: t.id, label: t.label, count: counts[t.id] ?? 0 }));
  if (hits.length === 0) return null;
  return { hits, total: hits.reduce((n, h) => n + h.count, 0) };
}

/**
 * The placeholder names the scope OUT LOUD, so it is never something you
 * have to infer: "Search 12,481 facts". Falls back to the bare label when
 * there is no count to quote.
 */
export function scopePlaceholder(label: string, count?: number): string {
  const noun = label.toLowerCase();
  if (count === undefined || count === null) return `Search ${noun}`;
  return `Search ${count.toLocaleString()} ${noun}`;
}
