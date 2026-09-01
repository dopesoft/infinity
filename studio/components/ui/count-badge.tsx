"use client";

import { cn } from "@/lib/utils";

/**
 * CountBadge — the one number chip.
 *
 * This shape was hand-rolled in three places (the Settings section rail, the
 * Accounts "Active" sub-tab, the Approvals sub-tabs) with the same dozen
 * classes copied each time, and they had already drifted: the Settings chip
 * painted `text-background` on an active tab whose ground IS `--background`,
 * so the number was invisible on the tab you were standing on. A shape that
 * appears on more than one screen belongs to a primitive, so it lives here
 * and every consumer routes through it.
 *
 * Two disciplines the primitive owns, so no consumer has to remember them:
 *
 *   - A ZERO IS NOT A BADGE. A chip reading "0" is furniture: it says
 *     nothing the empty list underneath does not already say, and it makes
 *     every row of a rail carry a number whether or not it means anything.
 *     Returning null here is why a caller can pass a count straight through.
 *   - The active treatment is ink on a wash of ink, never a named colour, so
 *     it reads on a primary pill (ground `--background` / `--accent` in
 *     dark) and under a sub-tab underline (ground: the page) alike.
 */
export function CountBadge({
  count,
  /** True when the chip sits on the tab/row you are currently on. */
  active,
  /** Noun for the screen reader: "8 accounts". Defaults to item/items. */
  noun,
  className,
}: {
  count: number;
  active?: boolean;
  noun?: string;
  className?: string;
}) {
  if (!count) return null;
  const word = noun ?? (count === 1 ? "item" : "items");
  return (
    <span
      className={cn(
        "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none tabular-nums",
        active
          ? "bg-foreground/10 text-foreground"
          : "bg-muted-foreground/15 text-muted-foreground",
        className,
      )}
      aria-label={`${count} ${word}`}
    >
      {count}
    </span>
  );
}
