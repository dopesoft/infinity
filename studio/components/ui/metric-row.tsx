import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * MetricRow — a number in a ledger, not a number in a box (Majordomo §5).
 *
 * Replaces the `MetricCard` tiles on /memory. A metric is a label and a
 * figure; it does not need a border, a rounded corner, or a background to be
 * legible. Stack several and the hairline between them does all the
 * separating work, which is what §1.2 asks for.
 *
 * Contract:
 *  - Label in chrome (13.5px, medium), value in mono + `tabular-nums` so a
 *    stacked column of figures aligns on the digit, not the glyph.
 *  - `tone` colours ONLY the figure, never the row ground — colour marks the
 *    one thing that is alive, waiting, or broken (§1.4).
 *  - `meta` is an optional quiet suffix ("of 1,204", "last 24h").
 *  - Each row draws its own bottom hairline and the last one drops it, so a
 *    group needs no wrapper chrome.
 *
 * Server-safe: no hooks, no handlers.
 */
export type MetricTone = "default" | "brand" | "info" | "warning" | "danger" | "quiet";

const VALUE_TONE: Record<MetricTone, string> = {
  default: "text-foreground",
  brand: "text-brand",
  info: "text-info",
  warning: "text-warning",
  danger: "text-danger",
  quiet: "text-quiet",
};

export interface MetricRowProps {
  label: React.ReactNode;
  value: number | string;
  /** Quiet suffix after the figure: a unit, a denominator, a window. */
  meta?: React.ReactNode;
  /** Colours the figure only. */
  tone?: MetricTone;
  /** Drop the bottom hairline. */
  noRule?: boolean;
  className?: string;
}

export function MetricRow({
  label,
  value,
  meta,
  tone = "default",
  noRule,
  className,
}: MetricRowProps) {
  return (
    <div
      className={cn(
        "flex min-h-11 min-w-0 max-w-full items-baseline justify-between gap-3 py-2.5",
        !noRule && "border-b border-hairline last:border-b-0",
        className,
      )}
    >
      <span className="min-w-0 truncate font-sans text-[13.5px] font-medium text-muted-foreground">
        {label}
      </span>
      <span className="flex shrink-0 items-baseline gap-1.5">
        <span className={cn("font-mono text-[15px] tabular-nums", VALUE_TONE[tone])}>{value}</span>
        {meta ? <span className="text-[12px] tabular-nums text-quiet">{meta}</span> : null}
      </span>
    </div>
  );
}
