"use client";

import * as React from "react";
import Link from "next/link";
import { motion } from "framer-motion";
import { ChevronRight } from "lucide-react";
import { ScrollList } from "@/components/dashboard/ScrollList";
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

const COLS: Record<2 | 3 | 4, string> = {
  2: "sm:grid-cols-2",
  3: "sm:grid-cols-3",
  // Four is the status board: one column per state. It goes straight from
  // stacked to four at `lg`, with no 2-up middle state, and that is
  // deliberate. The column hairline is a per-card `border-r` that only knows
  // DOM order, so at 2-up it would draw a rule down the middle of row two and
  // pad the third card as though it started a row. Wrapping needs per-row
  // edges, which a card cannot know. Below `lg` the stack IS the right shape
  // anyway - a phone has one column of room, and the old fix for that was a
  // snap-rail that hid three quarters of the work behind a swipe.
  4: "lg:grid-cols-4",
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
    <div
      className={cn(
        // grid-cols-1 default is REQUIRED: an implicit max-content track can
        // blow the column past the viewport on a phone.
        "grid min-w-0 grid-cols-1 gap-6 sm:gap-0",
        COLS[columns],
        className,
      )}
    >
      {children}
    </div>
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
    <div className="flex min-w-0 items-center gap-2 border-b border-border pb-1.5">
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
 */
export function BoardBand({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("-mx-4 min-w-0 bg-muted px-4 py-5 sm:-mx-6 sm:px-6", className)}>
      {children}
    </div>
  );
}
