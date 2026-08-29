/**
 * The next-24-hours projection behind <DayRibbon>.
 *
 * Pure and testable on purpose: "is tonight covered" is the question the
 * Automations page exists to answer, and getting a mark 6% off puts a job on
 * the wrong side of midnight. No React, no Date.now() inside — the caller
 * passes `now`, which is also what keeps the component deterministic in a
 * test and hydration-safe in the app.
 */

export const RIBBON_WINDOW_MS = 24 * 60 * 60 * 1000;

/**
 * Where a moment sits across the window, 0..100.
 * Returns null when it falls outside — an event that is not in the next 24h
 * has no position, and inventing one (by clamping) would draw a job at
 * midnight that actually fires next week.
 */
export function positionPct(now: number, at: number, windowMs = RIBBON_WINDOW_MS): number | null {
  if (!Number.isFinite(now) || !Number.isFinite(at)) return null;
  const delta = at - now;
  if (delta < 0 || delta > windowMs) return null;
  return (delta / windowMs) * 100;
}

/** A tick every `everyHours`, as {pct, label}. Labels are short and lowercase. */
export function ticks(
  now: number,
  everyHours = 6,
  windowMs = RIBBON_WINDOW_MS,
): { pct: number; label: string }[] {
  const out: { pct: number; label: string }[] = [];
  const stepMs = everyHours * 60 * 60 * 1000;
  for (let t = 0; t <= windowMs; t += stepMs) {
    out.push({ pct: (t / windowMs) * 100, label: hourLabel(now + t) });
  }
  return out;
}

/** "9am", "3pm", "12am". Short casual time, never 24h or ISO. */
export function hourLabel(ms: number): string {
  const h = new Date(ms).getHours();
  const suffix = h < 12 ? "am" : "pm";
  const twelve = h % 12 === 0 ? 12 : h % 12;
  return `${twelve}${suffix}`;
}

export type RibbonMark = {
  id: string;
  /** Plain English. Never a cron expression or a skill id. */
  label: string;
  at: number;
  /** brand = due and healthy · warning = needs you · danger = failed last time */
  tone?: "default" | "brand" | "warning" | "danger";
};

export type PlacedMark = RibbonMark & { pct: number };

/**
 * Place marks and drop the ones outside the window. Sorted by time so a
 * consumer can lay labels out left to right without re-sorting.
 *
 * `minGapPct` thins marks that would collide: with two events four minutes
 * apart the labels overlap into mush, so the LATER one is dropped and
 * reported in `hidden` — never silently, because a schedule page that
 * quietly omits a job is worse than one that admits it ran out of room.
 */
export function placeMarks(
  now: number,
  marks: RibbonMark[],
  opts: { windowMs?: number; minGapPct?: number } = {},
): { placed: PlacedMark[]; hidden: RibbonMark[] } {
  const windowMs = opts.windowMs ?? RIBBON_WINDOW_MS;
  const minGap = opts.minGapPct ?? 7;

  const inWindow: PlacedMark[] = [];
  const hidden: RibbonMark[] = [];
  for (const m of marks) {
    const pct = positionPct(now, m.at, windowMs);
    if (pct === null) hidden.push(m);
    else inWindow.push({ ...m, pct });
  }
  inWindow.sort((a, b) => a.pct - b.pct);

  const placed: PlacedMark[] = [];
  for (const m of inWindow) {
    const last = placed[placed.length - 1];
    if (last && m.pct - last.pct < minGap) {
      hidden.push(m);
      continue;
    }
    placed.push(m);
  }
  return { placed, hidden };
}
