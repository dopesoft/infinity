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
 */

export function ScrollList({
  max = 4,
  className,
  children,
}: {
  /** Number of rows to keep fully visible. Defaults to 4. */
  max?: number;
  className?: string;
  children: React.ReactNode;
}) {
  const ref = React.useRef<HTMLDivElement | null>(null);
  const [maxH, setMaxH] = React.useState<number | null>(null);

  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      // Find the row container (a <ul> or first child div). The wrapper
      // div may have its own padding; the rows live one level deeper.
      const rowParent =
        el.querySelector(":scope > ul, :scope > ol, :scope > div") ?? el;
      const rows = Array.from(rowParent.children) as HTMLElement[];
      if (rows.length <= max) {
        setMaxH(null);
        return;
      }
      // Sum the offsetHeight of the first `max` rows + gap-style margins
      // between them. offsetTop of the (max)-th row relative to the
      // parent gives us the exact pixel boundary at which to clip.
      const limitRow = rows[max];
      if (!limitRow) {
        setMaxH(null);
        return;
      }
      const top = limitRow.offsetTop;
      // Subtract a small slice so the clipped row's top edge peeks
      // through, telling the reader there's more below.
      setMaxH(Math.max(top - 8, 80));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [max, children]);

  const clipped = maxH !== null;

  return (
    <div
      ref={ref}
      className={cn(
        "relative min-w-0 max-w-full",
        clipped ? "overflow-y-auto scroll-touch overscroll-contain" : "",
        // Subtle fade mask at the bottom when clipped, hinting "more below".
        clipped
          ? "[mask-image:linear-gradient(to_bottom,black_calc(100%-24px),transparent)]"
          : "",
        className,
      )}
      style={maxH !== null ? { maxHeight: maxH } : undefined}
    >
      {children}
    </div>
  );
}
