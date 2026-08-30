"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * Timeline — the river shape.
 *
 * WHY THIS SHAPE
 *
 * Activity answers "what broke, and what has he been doing". Time is the
 * only sensible order for that, so time is the structure: one column, one
 * rail, the clock down the left in mono so your eye lands on WHEN before
 * WHAT. Jobs, conversations and things he noticed sit in one river because
 * you never wanted them separated, you wanted them in order.
 *
 * Red in the rail is the only decoration on the page. A failure is a dot
 * colour and a sentence, never a badge repeating the word "error" beside a
 * red dot that already said it.
 *
 * MOBILE: the same shape with a tighter gutter. Nothing is dropped, because
 * a phone is where you actually check whether last night went wrong.
 */

export type TimelineTone = "default" | "brand" | "warning" | "danger";

const DOT: Record<TimelineTone, string> = {
  default: "bg-quiet",
  brand: "bg-brand",
  warning: "bg-warning",
  danger: "bg-danger",
};

export function Timeline({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return <div className={cn("flex min-w-0 flex-col", className)}>{children}</div>;
}

/** A day heading. Mono, quiet, and the only thing that breaks the river. */
export function TimelineDay({ label }: { label: string }) {
  return (
    <div className="min-w-0 pb-1.5 pt-4 font-mono text-[9.5px] uppercase tracking-[0.12em] text-quiet first:pt-1">
      {label}
    </div>
  );
}

export function TimelineRow({
  /** Short casual time: "06:00", "now". Never an ISO string. */
  time,
  tone = "default",
  live,
  title,
  /** At most ONE line. Everything else is in the sheet. */
  meta,
  /** Duration or count, mono, right-aligned. */
  trailing,
  /** One action, only when the row can actually be resolved from here. */
  action,
  onClick,
  className,
}: {
  time: string;
  tone?: TimelineTone;
  live?: boolean;
  title: React.ReactNode;
  meta?: React.ReactNode;
  trailing?: React.ReactNode;
  action?: React.ReactNode;
  onClick?: () => void;
  className?: string;
}) {
  const body = (
    <>
      <span className="pt-0.5 text-right font-mono text-[10.5px] tabular-nums text-quiet">
        {time}
      </span>

      {/* The rail. The connecting line is drawn by the row so it runs
          continuously without a wrapper element per group. */}
      <span className="relative flex justify-center pt-1.5" aria-hidden>
        <span className="absolute -bottom-3.5 -top-2.5 w-px bg-hairline" />
        <span
          className={cn(
            "relative z-[1] size-[7px] rounded-full ring-[3px] ring-background",
            DOT[tone],
            live && "animate-pulse",
          )}
        />
      </span>

      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="min-w-0 break-words text-[13px] leading-snug">{title}</span>
        {/* Two lines, wrapped — not one truncated line. These carry humanised
            failure sentences ("the plan has used up its allowance, so it
            turned me away"), and a sentence cut mid-word at the right edge
            tells you less than nothing: you can see there was a reason and
            not what it was. Two lines is enough for every one of them, and
            the row is still a fixed, scannable height. */}
        {meta ? (
          <span className="min-w-0 break-words text-[11px] leading-snug text-quiet line-clamp-2">
            {meta}
          </span>
        ) : null}
      </span>

      <span className="flex shrink-0 items-start gap-2 pt-0.5">
        {trailing ? (
          <span className="font-mono text-[10.5px] tabular-nums text-quiet">{trailing}</span>
        ) : null}
        {action}
      </span>
    </>
  );

  const grid =
    "grid min-h-row min-w-0 grid-cols-[2.5rem_1rem_minmax(0,1fr)_auto] items-start gap-2.5 py-2 sm:grid-cols-[2.875rem_1rem_minmax(0,1fr)_auto] sm:gap-3";

  if (!onClick) {
    return <div className={cn(grid, className)}>{body}</div>;
  }
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(grid, "-mx-2 rounded-lg px-2 text-left transition-colors hover:bg-accent/60", className)}
    >
      {body}
    </button>
  );
}
