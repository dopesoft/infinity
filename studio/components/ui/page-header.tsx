import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * PageHeader — the ONE title on a route (Majordomo §5).
 *
 * Contract:
 *  - Renders the route's single `<h1>`: voice face, 26px, medium, tight
 *    tracking. If a page renders two of these, that page is a bug (§2, the
 *    depth rule: one title per surface).
 *  - `meta` is one 12.5px quiet line under the title — a count, a timestamp,
 *    a state. It is NOT a description of the title (§1.5); if the sentence
 *    only restates the title, drop it.
 *  - `actions` sit right-aligned on the title's baseline row and wrap under
 *    it on narrow screens rather than squeezing the title.
 *  - `live` adds the pulsing brand dot — the "one alive signal" (§1.4). Only
 *    true when something is genuinely happening right now. It LEADS the title
 *    in the voice register and TRAILS it in the display one, because the
 *    display register exists so the greeting can be the page's left edge: a
 *    7px glyph and its gap indent the title 17px, which is exactly the
 *    misalignment against the cards below that the register was introduced to
 *    fix. Measured, not assumed — h1 at 133px against a page column at 116.
 *  - `titleFace` picks the register the h1 is set in. `voice` is every
 *    route. `display` is Instrument Serif, and by contract (MAJORDOMO §4,
 *    amended 2026-08-30) belongs to the dashboard greeting alone — the one
 *    place on the product where a person is being spoken to rather than a
 *    surface being labelled. Both sizes live HERE rather than being passed
 *    in as a className, so the type law stays in one file.
 *
 * Server-safe: no hooks, no event handlers of its own. `actions` may be any
 * node, including a client component.
 *
 * Mobile-first: the title truncates to two lines rather than pushing the
 * page wider; `min-w-0` on the text column is load-bearing for that.
 */
export interface PageHeaderProps {
  /** The route's single h1. Plain string so it can never smuggle in chrome. */
  title: string;
  /** One quiet line under the title: count, timestamp, state. Never a description. */
  meta?: React.ReactNode;
  /** Right-aligned controls. Wraps below the title on narrow viewports. */
  actions?: React.ReactNode;
  /** Pulsing brand dot before the title — reserved for "happening right now". */
  live?: boolean;
  /** Register for the h1. `display` is the dashboard greeting only. */
  titleFace?: "voice" | "display";
  className?: string;
}

// A serif at the same nominal size reads optically smaller than Geist, and
// Instrument Serif ships Regular only — a synthesized `font-medium` thickens
// it into something else entirely. Hence the larger size and no weight.
const titleFaceClass: Record<"voice" | "display", string> = {
  voice: "font-voice text-[26px] font-medium leading-tight tracking-tight",
  display: "font-display text-[30px] leading-tight tracking-normal sm:text-[34px]",
};

function LiveDot() {
  return (
    <span
      className="size-[7px] shrink-0 animate-pulse-soft rounded-full bg-brand"
      aria-hidden
    />
  );
}

export function PageHeader({
  title,
  meta,
  actions,
  live,
  titleFace = "voice",
  className,
}: PageHeaderProps) {
  return (
    <header
      className={cn(
        "flex min-w-0 max-w-full flex-wrap items-start justify-between gap-x-4 gap-y-3 pb-4 pt-1",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex min-w-0 items-center gap-2.5">
          {live && titleFace !== "display" ? <LiveDot /> : null}
          <h1 className={cn("min-w-0 text-foreground", titleFaceClass[titleFace])}>
            {title}
          </h1>
          {live && titleFace === "display" ? <LiveDot /> : null}
        </div>
        {meta ? (
          <p className="min-w-0 text-[12.5px] leading-relaxed text-quiet">{meta}</p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}
