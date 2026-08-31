"use client";

import * as React from "react";
import Link from "next/link";
import { motion } from "framer-motion";
import { ChevronRight } from "lucide-react";
import { ScrollList } from "@/components/dashboard/ScrollList";
import { BAND_GROUND } from "@/components/dashboard/Section";
import { DASHBOARD_LIST_ROWS } from "@/components/dashboard/listHeight";
import { cn } from "@/lib/utils";

/**
 * Board + BoardCard — the glance shape.
 *
 * WHY THIS IS A PRIMITIVE, AND WHY ONLY HOME USES IT
 *
 * A board answers "what is going on right now, across everything" — several
 * kinds of thing at once, none of them deeply. That is exactly one page:
 * Home. Making every page a three-column board was the mistake this file is
 * the correction to; the other five pages ask different questions and get
 * different shapes (`PickList`, `SearchPage`, `DayRibbon`, `Timeline`).
 *
 * The load-bearing rules, all owned here so no consumer can drop one:
 *
 *   • A column boundary carries the TYPE. One card holds one kind of thing.
 *     That is what stops you being three rows into a section you did not
 *     know you had entered, reading it the wrong way — a 10px group label is
 *     too weak a signal to carry a type change, a column is not.
 *   • Four rows, then it scrolls inside itself, via the existing
 *     <ScrollList>. Nothing below the fold on a laptop.
 *   • The bottom fade is a PROMISE that there is more. ScrollList only
 *     applies it when the row count actually exceeds the cap, so a two-row
 *     card never looks truncated.
 *   • The title is the link into that type's own list. A count that is not a
 *     tap target is decoration.
 *
 * MOBILE: one column, borders off, gap on. Checked at 375.
 */

/* The layout per column count, complete rather than concatenated: `flex` and
 * `grid` are both unprefixed display utilities, so mixing them across two
 * strings would leave the winner up to stylesheet order. Each entry here owns
 * its whole display story, and every override across it is behind a
 * breakpoint, where the cascade is deterministic. */
const LAYOUT: Record<2 | 3 | 4, string> = {
  2: "grid min-w-0 grid-cols-1 gap-6 sm:grid-cols-2 sm:gap-0",
  3: "grid min-w-0 grid-cols-1 gap-6 sm:grid-cols-3 sm:gap-0",

  /* Four is the status board, and it is the one shape that SCROLLS SIDEWAYS on
   * a phone rather than stacking.
   *
   * Stacking four columns puts three screens of scrolling between "what is
   * running" and "what finished", and reading a board vertically is reading a
   * list - which is the thing a board exists not to be. The old mobile version
   * did scroll sideways but drove it with pager dots and prev/next buttons,
   * and that was the actual problem: a column was either fully on screen or
   * completely invisible, with a widget in between telling you it existed.
   *
   * A column is 78vw here, so the next one is always peeking at the edge. The
   * peek IS the affordance - it says "there is more that way" continuously and
   * without a control - and snap points mean a swipe lands a column square
   * instead of halfway. Nothing is hidden behind a button.
   *
   * `overscroll-x-contain` stops a swipe past the last column from chaining
   * into the page, which on iOS is what triggers back-navigation. */
  4: [
    "flex min-w-0 snap-x snap-mandatory gap-4 overflow-x-auto overscroll-x-contain scroll-touch no-scrollbar pb-1",
    "[&>*]:w-[78vw] [&>*]:max-w-[19rem] [&>*]:shrink-0 [&>*]:snap-start",
    /* The card draws its own column hairline for the grid layouts. In the
     * scroller the gap does that job and a trailing rule would just hang off
     * each card, so the parent overrides it - `!` because a parent overriding
     * a child's layout is exactly the case the cascade cannot settle on its
     * own, both being one class deep at the same breakpoint. */
    "[&>*]:!border-r-0 [&>*]:!px-0",
    /* Back to a real grid the moment there is room for four abreast. */
    "lg:grid lg:grid-cols-4 lg:gap-0 lg:overflow-visible lg:pb-0",
    "lg:[&>*]:w-auto lg:[&>*]:max-w-none",
    "lg:[&>*]:!border-r lg:[&>*]:!px-4 lg:[&>*:first-child]:!pl-0",
    "lg:[&>*:last-child]:!border-r-0 lg:[&>*:last-child]:!pr-0",
  ].join(" "),
};

export function Board({
  columns = 3,
  className,
  children,
}: {
  /** 3 by default; 2 for a pair; 4 for a status board. Past `max-w-board` add
   *  a column, never widen rows. */
  columns?: 2 | 3 | 4;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    // grid-cols-1 as the stacked default is REQUIRED on the grid layouts: an
    // implicit max-content track can blow a column past the viewport on a
    // phone. See LAYOUT.
    <div className={cn(LAYOUT[columns], className)}>{children}</div>
  );
}

export function BoardCard({
  title,
  count,
  href,
  action,
  seeAll,
  rows = DASHBOARD_LIST_ROWS,
  /** Stagger index. Cards fade in 40ms apart; fades only, per Majordomo §4. */
  delay = 0,
  /** Content between the head and the list, OUTSIDE the clip: a banner, an
   *  inline form. It must not scroll away with the rows. */
  lead,
  /** Shown instead of the list when there is nothing. Say what will fill it. */
  empty,
  /** One quiet action under the list ("+ Add todo"). Renders whether or not
   *  the card is empty - you must be able to add the first one. */
  footer,
  className,
  children,
}: {
  title: string;
  /** Shown beside the title. Omit when the card is a summary, not a list. */
  count?: number | string;
  /** Where the title leads: that one type, on its own, with its own search. */
  href?: string;
  /** Right-aligned control in the head (e.g. "add"). Falls back to a chevron when `href` is set. */
  action?: React.ReactNode;
  /** Footer link text. Only render it when there is genuinely more to see. */
  seeAll?: { label: string; href: string };
  rows?: number;
  delay?: number;
  lead?: React.ReactNode;
  empty?: React.ReactNode;
  footer?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  const head = (
    /* min-h-10, and it is load-bearing rather than cosmetic.
     *
     * The head had no floor, so its height was whatever its tallest child
     * happened to be. A card with nothing in `action` measured off the title
     * text (14.5px, about 22px of line box) and put its rule at ~28px; the
     * Phone card puts two `size-8` buttons in that slot and put its rule at
     * ~38px. Side by side in a Board that is three columns whose hairlines sit
     * on three different lines - which is exactly the misalignment the boss
     * reported, and the same bug SectionTitle already fixed with min-h-11.
     *
     * 40px is the floor because the tallest control a head may hold is 32px
     * (h-8 / size-8 everywhere in this codebase) and the box is border-box,
     * so 32 + 6px of pb-1.5 = 38 still fits under it. Every card's rule now
     * lands on the same line by construction, whether or not it has icons,
     * and the next card someone adds with a button in its head is correct
     * without anyone remembering this. */
    <div className="flex min-h-10 min-w-0 items-center gap-2 border-b border-border pb-1.5">
      <span className="truncate text-[14.5px] font-medium tracking-tight">{title}</span>
      {count !== undefined && count !== "" ? (
        <span className="shrink-0 font-mono text-[11px] tabular-nums text-quiet">{count}</span>
      ) : null}
      <span className="ml-auto flex shrink-0 items-center text-quiet">
        {action ?? (href ? <ChevronRight className="size-3.5" aria-hidden /> : null)}
      </span>
    </div>
  );

  const isEmpty = React.Children.toArray(children).length === 0;

  return (
    <motion.section
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.22, delay, ease: "easeOut" }}
      className={cn(
        // The cell: hairline between columns on desktop, nothing on mobile
        // where the columns become stacked sections.
        "flex min-w-0 flex-col gap-0.5 sm:border-r sm:border-hairline sm:px-4",
        "sm:first:pl-0 sm:last:border-r-0 sm:last:pr-0",
        className,
      )}
    >
      {href ? (
        <Link
          href={href}
          className="min-w-0 rounded-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {head}
        </Link>
      ) : (
        head
      )}

      {lead}

      {isEmpty && empty ? (
        <p className="py-2 text-[13px] text-quiet">{empty}</p>
      ) : (
        <ScrollList max={rows} className="min-w-0">
          <div className="flex min-w-0 flex-col">{children}</div>
        </ScrollList>
      )}

      {seeAll && !isEmpty ? (
        <Link
          href={seeAll.href}
          className="min-w-0 truncate pt-1.5 text-[11.5px] text-quiet transition-colors hover:text-foreground"
        >
          {seeAll.label}
        </Link>
      ) : null}

      {footer ? <div className="min-w-0">{footer}</div> : null}
    </motion.section>
  );
}

/**
 * BoardBand — a tinted full-bleed ground carrying one row of board cards.
 *
 * Sections separate by ground, not by chrome (Majordomo §1.2). Alternating a
 * plain row with a banded row is what stops a long page reading as one wall,
 * without putting a box around anything. The negative margins exactly cancel
 * the page column's padding so banding can never introduce horizontal scroll.
 *
 * The ground itself comes from `BAND_GROUND` in dashboard/Section.tsx. This
 * file used to carry its own copy of that class string, so the two bands on
 * the dashboard were one edit away from being different colours.
 */
export function BoardBand({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn(BAND_GROUND, className)}>{children}</div>
  );
}
