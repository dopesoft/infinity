"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * CountLine — plain tappable counts, separated by middots.
 *
 * WHAT THIS REPLACES, AND WHY
 *
 * A row of chips. Chips are for FILTERS: tap one and you see fewer of the
 * same kind of thing. When the "chips" are actually five different kinds
 * (facts, lessons, wrong guesses, observations, stale), each with its own
 * shape and its own actions, a chip row is the wrong control — it reads as
 * five buttons of equal weight and buries the number you came for inside a
 * pill. Set as plain text with the number in ink, the counts read as a
 * sentence and still navigate.
 *
 * This is the "million chips" fix, expressed once so no page re-rolls it.
 *
 * MOBILE: wraps naturally, no horizontal scroll, and each item keeps a
 * 44px-tall hit area via padding rather than a fixed height.
 */

export type CountLineItem = {
  /** The number. Pre-formatted by the caller ("12,481", "148k"). */
  value: string | number;
  /** What the number counts, lowercase: "facts", "lessons", "going stale". */
  label: string;
  onSelect?: () => void;
  href?: string;
  /** Marks the active scope. Underlined rather than filled. */
  selected?: boolean;
  /** Amber for the one that wants something from you. Nothing else is tinted. */
  tone?: "default" | "warning" | "danger";
};

export function CountLine({
  items,
  className,
}: {
  items: CountLineItem[];
  className?: string;
}) {
  const visible = items.filter((i) => i.value !== 0 || i.selected);
  if (visible.length === 0) return null;

  return (
    <div
      className={cn("flex min-w-0 flex-wrap items-center text-[12.5px] text-quiet", className)}
    >
      {visible.map((item, i) => (
        <React.Fragment key={`${item.label}-${i}`}>
          {i > 0 && (
            <span aria-hidden className="select-none px-2 text-border">
              ·
            </span>
          )}
          <CountItem item={item} />
        </React.Fragment>
      ))}
    </div>
  );
}

function CountItem({ item }: { item: CountLineItem }) {
  const tone =
    item.tone === "warning"
      ? "text-warning"
      : item.tone === "danger"
        ? "text-danger"
        : undefined;

  // An empty value is meaningful: some kinds have no count worth quoting
  // (a profile), and some only know their count once their view is open —
  // printing a stale "0" there would read as "he has none", which is a lie.
  const hasValue = item.value !== "" && item.value !== undefined && item.value !== null;
  const inner = (
    <>
      {hasValue ? (
        <>
          <span className={cn("font-medium tabular-nums", tone ?? "text-foreground")}>
            {item.value}
          </span>{" "}
        </>
      ) : null}
      <span className={hasValue ? tone : cn(tone ?? "text-foreground", "font-medium")}>
        {item.label}
      </span>
    </>
  );

  const cls = cn(
    "inline-flex min-h-row items-center whitespace-nowrap py-1 transition-colors",
    item.selected
      ? "border-b-[1.5px] border-foreground"
      : "border-b-[1.5px] border-transparent hover:text-foreground",
  );

  if (item.href) {
    // next/link is intentionally not used here: a CountLine item is as often
    // an in-page scope change as a route change, and mixing the two behind
    // one prop is how a "filter" silently becomes a navigation.
    return (
      <a href={item.href} className={cls}>
        {inner}
      </a>
    );
  }
  if (item.onSelect) {
    return (
      <button type="button" onClick={item.onSelect} aria-pressed={item.selected} className={cls}>
        {inner}
      </button>
    );
  }
  return <span className={cn(cls, "cursor-default")}>{inner}</span>;
}
