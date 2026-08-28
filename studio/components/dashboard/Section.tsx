"use client";

import * as React from "react";
import { motion } from "framer-motion";
import { ArrowRight, type LucideIcon } from "lucide-react";
import Link from "next/link";
import { cn } from "@/lib/utils";

/* Section + SectionTitle + TileCard — Majordomo §1.2 and §5.
 *
 * WHAT CHANGED, AND WHY
 *
 * Section used to be a rounded-2xl bordered card with a header strip and a
 * `border-t` divider, on every dashboard block. Stacked ten deep that reads
 * as a deck of boxes: chrome competing with content, and every list row then
 * needing its own box to look intentional inside it.
 *
 * The new rule is TONE, not boxes, ONE LEVEL DEEP:
 *
 *   tone="plain" (default)  the section sits on the page. Title row on a
 *                           hairline, then the rows. No container at all.
 *   tone="band"             the same section on a full-bleed `bg-muted`
 *                           band. Negative horizontal margins cancel the
 *                           page column's padding so the ground runs edge to
 *                           edge — the "web style" alternation that stops a
 *                           long page reading as one wall of text.
 *   tone="card"             a radius-16 `bg-card` bordered container, for ONE
 *                           object the boss acts on as a unit (a proposal, a
 *                           reflection awaiting a decision).
 *
 * Never nest tones: no card inside a card, no card on a band, no header bar
 * inside a card, no bordered rows inside any of them. Rows separate with a
 * hairline (see `ui/list-row.tsx`).
 *
 * Every existing prop still works — `title`, `Icon`, `badge`, `action`,
 * `headerExtra`, `delay`, `className`, `contentClassName`, `noPad`, so no
 * consumer had to change to compile. The page sweeps that pick tones per
 * section are a later phase.
 *
 * TileCard keeps its export and its `tone` prop for its four consumers, but
 * is now ListRow styling: a hairline-separated tappable row, no border, no
 * lift, no rounded box. Its consumers still render their own icon tiles;
 * those come off in the page sweep.
 */

export type SectionTone = "plain" | "band" | "card";

/**
 * The band ground, defined ONCE.
 *
 * Negative horizontal margins exactly cancel the page column's padding
 * (`px-4 sm:px-6`) so the muted ground runs edge to edge of the column while
 * the content box is unchanged — which is why banding can never introduce
 * horizontal scroll. Shared by `Section tone="band"` (one section on a band)
 * and `SectionBand` (a row of sections sharing one band), so the two can never
 * drift apart by a pixel.
 */
const BAND_GROUND = "-mx-4 min-w-0 bg-muted px-4 py-5 sm:-mx-6 sm:px-6";

/**
 * SectionBand — one muted band carrying SEVERAL plain sections.
 *
 * `Section tone="band"` bands a single section. A dashboard row is three
 * sections side by side that must read as ONE area (Calendar · Todos ·
 * Pursuits), and three separate bands would draw three grounds with gutters of
 * page between them. This is that shape: the same ground, wrapped around a row.
 *
 * The sections inside stay `plain` — one level of tone, always (§1.2). Nothing
 * inside a band may be a card.
 */
export function SectionBand({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return <div className={cn(BAND_GROUND, "max-w-full", className)}>{children}</div>;
}

/**
 * SectionTitle — the title row every Section tone shares.
 *
 * Voice face at 18px (§4 scale), the count/meta in quiet beside it, and an
 * optional "see all" link. Exported so a surface that composes its own
 * section shell (a modal, a split pane) uses the identical title rather than
 * inventing one.
 */
export function SectionTitle({
  title,
  Icon,
  badge,
  action,
  headerExtra,
  className,
}: {
  title: string;
  Icon?: LucideIcon;
  /** Count or short meta beside the title. A node so a locale-dependent
   *  stamp can bring its own `suppressHydrationWarning`. */
  badge?: React.ReactNode;
  action?: { label: string; href: string };
  headerExtra?: React.ReactNode;
  className?: string;
}) {
  const hasBadge = badge !== undefined && badge !== null && badge !== 0 && badge !== "";
  return (
    <div
      className={cn(
        "flex min-h-9 min-w-0 max-w-full items-center justify-between gap-3",
        className,
      )}
    >
      <div className="flex min-w-0 items-baseline gap-2">
        {Icon ? (
          <Icon className="size-4 shrink-0 self-center text-quiet" aria-hidden />
        ) : null}
        <h2 className="truncate font-voice text-[18px] font-medium tracking-tight text-foreground">
          {title}
        </h2>
        {hasBadge ? (
          <span className="shrink-0 font-mono text-[12px] tabular-nums text-quiet">{badge}</span>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {action ? (
          <Link
            href={action.href}
            className="group inline-flex h-8 items-center gap-1 rounded-lg px-2 text-[12.5px] font-medium text-quiet transition-colors hover:bg-accent/60 hover:text-foreground"
          >
            {action.label}
            <ArrowRight
              className="size-3 transition-transform group-hover:translate-x-0.5"
              aria-hidden
            />
          </Link>
        ) : null}
        {headerExtra}
      </div>
    </div>
  );
}

export function Section({
  title,
  Icon,
  badge,
  action,
  headerExtra,
  tone = "plain",
  delay = 0,
  className,
  contentClassName,
  noPad,
  children,
}: {
  title: string;
  Icon?: LucideIcon;
  /** Count or short meta beside the title. See SectionTitle. */
  badge?: React.ReactNode;
  action?: { label: string; href: string };
  /** Arbitrary right-aligned header control (e.g. the Phone card's dial
   *  button). Renders after `action` when both are present. */
  headerExtra?: React.ReactNode;
  /** Majordomo §5. `plain` (default) sits on the page, `band` on a full-bleed
   *  muted band, `card` in a radius-16 bordered container for ONE actionable
   *  object. Never nest tones. */
  tone?: SectionTone;
  delay?: number;
  className?: string;
  contentClassName?: string;
  /** Skip the default body padding when the children bring their own. */
  noPad?: boolean;
  children: React.ReactNode;
}) {
  const header = (
    <SectionTitle
      title={title}
      Icon={Icon}
      badge={badge}
      action={action}
      headerExtra={headerExtra}
      className="border-b border-hairline pb-2"
    />
  );

  const body = (
    <div className={cn("min-w-0 max-w-full", noPad ? "" : "pt-3", contentClassName)}>
      {children}
    </div>
  );

  // `band` bleeds past the page column's horizontal padding so the muted
  // ground runs edge to edge. The negative margin is exactly cancelled by the
  // matching padding, so the section's own content box is unchanged and no
  // horizontal scroll can appear (TabFrame's `overflow-x-hidden` is the
  // second belt).
  const toneClass: Record<SectionTone, string> = {
    plain: "min-w-0 max-w-full",
    band: BAND_GROUND,
    card: "min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card p-4 text-card-foreground sm:p-5",
  };

  return (
    <motion.section
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.35, delay, ease: [0.2, 0.7, 0.2, 1] }}
      className={cn(toneClass[tone], className)}
    >
      {header}
      {body}
    </motion.section>
  );
}

/**
 * TileCard — kept for its existing consumers (ApprovalsCard, FollowUpsCard,
 * PursuitsCard, SurfaceCard), now implemented as ListRow styling: hairline
 * separator, 44px minimum height, hover ground, no border, no radius, no
 * lift. `tone` is still accepted; under Majordomo it no longer paints a
 * border on hover (colour is reserved for alive/waiting/broken), so it is
 * kept as an accepted-and-ignored prop until the page sweep removes the call
 * sites. Documented rather than deleted so nothing silently changes meaning.
 */
export const TileCard = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & {
    tone?: "default" | "accent" | "warning" | "info" | "success" | "danger";
  }
>(({ className, tone = "default", ...props }, ref) => {
  // `tone` is accepted and deliberately unused: it used to paint a hover
  // border, which Majordomo reserves for alive/waiting/broken. Consumed here
  // so it never reaches the DOM as an unknown attribute, and so removing it
  // from the four call sites stays a page-sweep concern rather than a
  // compile break.
  void tone;
  return (
    <button
      ref={ref}
      type="button"
      className={cn(
        "group relative flex min-h-11 w-full min-w-0 max-w-full items-center gap-3 border-b border-hairline py-2.5 text-left transition-colors last:border-b-0",
        "hover:bg-accent/60 active:bg-accent",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
        className,
      )}
      {...props}
    />
  );
});
TileCard.displayName = "TileCard";
