"use client";

import * as React from "react";
import { placeMarks, ticks, type RibbonMark } from "@/lib/schedule/ribbon";
import { cn } from "@/lib/utils";

/**
 * DayRibbon — the next twenty four hours, drawn once.
 *
 * WHY THIS SHAPE
 *
 * You do not open Automations to read a list, you open it to find out
 * whether tonight is covered. That question is about TIME, so the answer is
 * a timeline rather than a table. Schedules and watchers stop being separate
 * tabs here for the same reason: on a timeline they are the same thing, a
 * mark in the future.
 *
 * The red mark is where it went wrong last night; that is the one thing your
 * eye should land on before you read a single row underneath.
 *
 * HYDRATION: `now` is a prop, never `Date.now()` in render, so the server
 * and the first client paint agree. The caller sets it in an effect.
 *
 * MOBILE: scrolls horizontally inside its own container with a min-width, so
 * the labels never crush together and the page itself never scrolls sideways.
 *
 * INLINE STYLE, deliberately: the left offset of a tick or a mark is computed
 * from data (a timestamp projected onto the window) and cannot be a Tailwind
 * class. This is the same sanctioned exception as the Composer's calculated
 * textarea height — a computed VALUE, never a styling decision. Every colour,
 * size and spacing choice here is still a token class.
 */
export function DayRibbon({
  now,
  marks,
  className,
}: {
  /** Epoch ms for "now". Pass null before the client clock is known. */
  now: number | null;
  marks: RibbonMark[];
  className?: string;
}) {
  if (now == null) {
    // Reserve the height so the rows below do not jump when the clock lands.
    return <div className={cn("h-[60px] w-full", className)} aria-hidden />;
  }

  const { placed, hidden } = placeMarks(now, marks);
  const axis = ticks(now);

  return (
    <div className={cn("min-w-0", className)}>
      <div className="overflow-x-auto scroll-touch no-scrollbar">
        <div className="relative h-[60px] min-w-[520px]">
          {/* the axis */}
          <div className="absolute left-0 right-0 top-9 h-px bg-border" aria-hidden />
          {axis.map((t, i) => (
            <React.Fragment key={`${t.pct}-${i}`}>
              <span
                aria-hidden
                className="absolute top-9 h-[5px] w-px bg-border"
                style={edge(t.pct)}
              />
              <span
                className="absolute top-[45px] whitespace-nowrap font-mono text-[9.5px] tabular-nums text-quiet"
                style={edgeLabel(t.pct)}
              >
                {t.label}
              </span>
            </React.Fragment>
          ))}

          {/* now */}
          <span
            aria-hidden
            className="absolute bottom-3 left-0 top-3.5 w-px bg-brand"
          />

          {placed.map((m) => (
            <span
              key={m.id}
              className="absolute top-1.5 flex -translate-x-1/2 flex-col items-center gap-1 whitespace-nowrap"
              style={{ left: `${m.pct}%` }}
            >
              <span
                className={cn(
                  "text-[10px]",
                  m.tone === "danger" ? "text-danger" : "text-muted-foreground",
                )}
              >
                {m.label}
              </span>
              <span
                aria-hidden
                className={cn(
                  "size-[7px] rounded-full",
                  m.tone === "danger"
                    ? "bg-danger"
                    : m.tone === "warning"
                      ? "bg-warning"
                      : m.tone === "brand"
                        ? "bg-brand"
                        : "bg-quiet",
                )}
              />
            </span>
          ))}
        </div>
      </div>

      {/* Never omit silently: a schedule page that quietly drops a job is
          worse than one that says it ran out of room. */}
      {hidden.length > 0 ? (
        <p className="pt-1 text-[11px] text-quiet">
          {hidden.length} more not shown here, {hidden.length === 1 ? "it is" : "they are"} in the
          list below.
        </p>
      ) : null}
    </div>
  );
}

// The first and last tick sit ON the edges, so they anchor rather than
// centre — otherwise half the label hangs outside the ribbon.
function edge(pct: number): React.CSSProperties {
  if (pct <= 0) return { left: 0 };
  if (pct >= 100) return { right: 0 };
  return { left: `${pct}%` };
}
function edgeLabel(pct: number): React.CSSProperties {
  if (pct <= 0) return { left: 0 };
  if (pct >= 100) return { right: 0 };
  return { left: `${pct}%`, transform: "translateX(-50%)" };
}
