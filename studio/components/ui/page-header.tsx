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
 *  - `live` prepends the pulsing brand dot — the "one alive signal" (§1.4).
 *    Only true when something is genuinely happening right now.
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
  className?: string;
}

export function PageHeader({ title, meta, actions, live, className }: PageHeaderProps) {
  return (
    <header
      className={cn(
        "flex min-w-0 max-w-full flex-wrap items-start justify-between gap-x-4 gap-y-3 pb-4 pt-1",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex min-w-0 items-center gap-2.5">
          {live ? (
            <span
              className="size-[7px] shrink-0 animate-pulse-soft rounded-full bg-brand"
              aria-hidden
            />
          ) : null}
          <h1 className="min-w-0 font-voice text-[26px] font-medium leading-tight tracking-tight text-foreground">
            {title}
          </h1>
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
