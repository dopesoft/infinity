"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

/* ScrollList - cap a list to the height of its first N children and
 * scroll the rest internally.
 *
 * Why this exists: every dashboard list card (Follow-ups, Approvals,
 * Saved, Activity, Todos) used to render every row, pushing the page
 * to absurd heights when the agent surfaced 30 emails. The fix is
 * structural, not per-card: pass `max` and the primitive measures the
 * first `max` direct children after mount and clamps the container
 * height to that. Anything beyond scrolls with inertial touch and a
 * fade mask cuing "more below". Consumers stay simple - wrap the
 * existing `<ul>` (or `<div>` of rows) and add `max={N}`.
 *
 * Reuse-first componentization (Studio Rule #2): the discipline lives
 * here. New list cards adopt this in one line.
 *
 * Two modes, so sibling cards in the same row can line their fades up:
 *   • Default (row-count): pass `max` and the list measures the first
 *     `max` direct children and clamps to that pixel height. It also
 *     reports that pixel height back via `onMeasure` so a neighbour can
 *     consume it.
 *   • Matched (pixel): pass `maxHeight` (px) and the list clamps to that
 *     exact height regardless of its own row heights, fading only when
 *     its content actually overflows. This is how a card with SHORT rows
 *     (Upcoming) lines its fade up with a card of TALL rows (Follow-ups)
 *     instead of clipping after 3 items — row counts can't align cards
 *     whose rows are different heights; a shared pixel height can.
 */

export function ScrollList({
  max = 4,
  maxHeight,
  onMeasure,
  className,
  children,
}: {
  /** Number of rows to keep fully visible. Defaults to 4. Ignored when `maxHeight` is set. */
  max?: number;
  /**
   * Explicit pixel cap. When set, the list clamps here directly (matched
   * mode) and fades only if its content overflows. Takes precedence over
   * `max`. Pass null/undefined to fall back to row-count measurement.
   */
  maxHeight?: number | null;
  /**
   * Called with the measured row-count clip height (px) in default mode,
   * or null when the list is short enough not to clip. Lets a sibling
   * list match this one's fade line exactly. No-op in matched mode.
   */
  onMeasure?: (px: number | null) => void;
  className?: string;
  children: React.ReactNode;
}) {
  const ref = React.useRef<HTMLDivElement | null>(null);
  const [measuredH, setMeasuredH] = React.useState<number | null>(null);
  const [overflowing, setOverflowing] = React.useState(false);
  const matched = maxHeight != null;

  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      if (matched) {
        // Matched mode: the cap is given; we only need to know whether the
        // content overflows it so the fade + scroll turn on. +1 guards
        // sub-pixel rounding so a list that exactly fits doesn't fade.
        setOverflowing(el.scrollHeight > (maxHeight as number) + 1);
        return;
      }
      // Find the row container (a <ul> or first child div). The wrapper
      // div may have its own padding; the rows live one level deeper.
      const rowParent =
        el.querySelector(":scope > ul, :scope > ol, :scope > div") ?? el;
      const rows = Array.from(rowParent.children) as HTMLElement[];
      if (rows.length <= max) {
        setMeasuredH(null);
        onMeasure?.(null);
        return;
      }
      // Sum the offsetHeight of the first `max` rows + gap-style margins
      // between them. offsetTop of the (max)-th row relative to the
      // parent gives us the exact pixel boundary at which to clip.
      const limitRow = rows[max];
      if (!limitRow) {
        setMeasuredH(null);
        onMeasure?.(null);
        return;
      }
      const top = limitRow.offsetTop;
      // Subtract a small slice so the clipped row's top edge peeks
      // through, telling the reader there's more below.
      const h = Math.max(top - 8, 80);
      setMeasuredH(h);
      onMeasure?.(h);
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [max, maxHeight, matched, children, onMeasure]);

  const effectiveH = matched ? (maxHeight as number) : measuredH;
  const clipped = matched ? overflowing : measuredH !== null;

  return (
    <div
      ref={ref}
      className={cn(
        "relative min-w-0 max-w-full",
        // pt-px keeps the first row's top border + focus ring + hover
        // -1px translate visible. Without it, overflow-y-auto clips the
        // first item flush at y=0 and the user sees three sides of the
        // border but not the top. px-px gives focus-ring breathing room
        // on the sides too.
        clipped
          ? "overflow-y-auto scroll-touch overscroll-contain px-px pt-px"
          : "",
        // Subtle fade mask at the bottom when clipped, hinting "more below".
        clipped
          ? "[mask-image:linear-gradient(to_bottom,black_calc(100%-24px),transparent)]"
          : "",
        className,
      )}
      style={effectiveH != null ? { maxHeight: effectiveH } : undefined}
    >
      {children}
    </div>
  );
}
