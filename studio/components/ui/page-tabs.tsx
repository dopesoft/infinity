"use client";

/**
 * Standard page-level tab + filter primitives. Use these everywhere a page
 * has a top-level view switcher with optional sub-filters underneath.
 *
 *   <PageTabs value=… onValueChange=…>
 *     <PageTabsList>
 *       <PageTabsTrigger value="all">All</PageTabsTrigger>
 *       <PageTabsTrigger value="active">Active</PageTabsTrigger>
 *     </PageTabsList>
 *   </PageTabs>
 *
 *   <FilterPillRow>
 *     <FilterPill active={tier === "all"} onClick={…}>all</FilterPill>
 *     <FilterPill active={tier === "low"} onClick={…}>low</FilterPill>
 *   </FilterPillRow>
 *
 *   <HScrollRow>  (generic horizontal-scroll row for cards / chips)
 *     {items.map(...)}
 *   </HScrollRow>
 *
 * Sizing rules (must match across the app):
 *   PageTabsList    h-9, full-width grid on mobile, inline on sm+
 *   PageTabsTrigger font-mono text-[11px] uppercase tracking-wider
 *   FilterPill      h-8 px-3.5 rounded-full font-mono text-[11px] uppercase
 *   FilterPillRow   gap-2 py-1, horizontal-scroll on mobile, flex-wrap on sm+
 *
 * Layout decision tree (pick exactly one):
 *   • `columns={2|3}` with short text-only labels (e.g. "Cron" / "Sentinels")
 *     → full-width equal-cell grid on mobile, inline on sm+. Looks crisp.
 *   • `scrollable` for ANY of: 4+ tabs, labels with count badges, labels with
 *     variable-length text. Horizontal-scroll on mobile (no squishing, no
 *     clipping); inline-flex on sm+. THIS IS THE DEFAULT FOR ANYTHING WITH
 *     COUNTS - `columns={N}` + count badges crushes the labels on a 375px
 *     viewport. If you're unsure, use `scrollable`.
 *   • Neither prop → inline-flex everywhere (caller controls width).
 *
 * Don't deviate from these; if a page needs a different look, change them
 * here so every screen moves together.
 */

import * as React from "react";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export const PageTabs = Tabs;

// Static lookup so Tailwind's JIT keeps these classes in the bundle.
// Don't switch to template-literal interpolation - JIT can't resolve those.
const COLUMN_LAYOUTS: Record<number, string> = {
  2: "grid w-full grid-cols-2 sm:inline-flex sm:w-auto",
  3: "grid w-full grid-cols-3 sm:inline-flex sm:w-auto",
  4: "grid w-full grid-cols-4 sm:inline-flex sm:w-auto",
  5: "grid w-full grid-cols-5 sm:inline-flex sm:w-auto",
  6: "grid w-full grid-cols-6 sm:inline-flex sm:w-auto",
};

// Horizontal-scroll mode for mobile: inline-flex with overflow-x-auto, every
// trigger pinned `shrink-0` so labels (incl. count badges) never get crushed.
// Snap proximity is gentle enough to glance but still helps land on a tab.
// no-scrollbar + scroll-touch keep it native-feeling on iOS. On sm+ we go
// back to inline-flex with overflow visible - desktop has room.
const SCROLLABLE_LAYOUT =
  "inline-flex w-full max-w-full justify-start overflow-x-auto scroll-touch snap-x snap-proximity no-scrollbar [&>*]:shrink-0 [&>*]:snap-start sm:w-auto sm:max-w-none sm:overflow-visible";

export const PageTabsList = React.forwardRef<
  React.ElementRef<typeof TabsList>,
  React.ComponentPropsWithoutRef<typeof TabsList> & {
    columns?: number;
    scrollable?: boolean;
  }
>(({ className, columns, scrollable, children, ...props }, ref) => {
  // Precedence: `scrollable` wins over `columns`. If neither is set we default
  // to inline-flex everywhere (caller can still pass classes via className).
  // `columns` is only safe for 2–3 short text-only labels - anything with a
  // count badge or 4+ tabs squishes on mobile (375px / 6 ≈ 62px per cell).
  let layout: string;
  if (scrollable) {
    layout = SCROLLABLE_LAYOUT;
  } else if (columns) {
    layout = COLUMN_LAYOUTS[columns] ?? "inline-flex";
  } else {
    layout = "inline-flex";
  }
  return (
    <TabsList ref={ref} className={cn("h-9", layout, className)} {...props}>
      {children}
    </TabsList>
  );
});
PageTabsList.displayName = "PageTabsList";

export const PageTabsTrigger = React.forwardRef<
  React.ElementRef<typeof TabsTrigger>,
  React.ComponentPropsWithoutRef<typeof TabsTrigger>
>(({ className, ...props }, ref) => (
  <TabsTrigger
    ref={ref}
    className={cn(
      "font-mono text-[11px] uppercase tracking-wider",
      className,
    )}
    {...props}
  />
));
PageTabsTrigger.displayName = "PageTabsTrigger";

/**
 * Horizontal-scroll row that goes edge-to-edge on mobile (so cards/chips
 * scroll flush to the screen edge) and behaves as flex-wrap on sm+. Pair
 * with `<FilterPill>` for chip rails or with `<MetricCard className="snap-start min-w-[10.5rem] shrink-0 sm:min-w-0" />`
 * for analytics card rows.
 */
export function HScrollRow({
  children,
  className,
  wrap = true,
  edgeBleed = true,
}: {
  children: React.ReactNode;
  className?: string;
  wrap?: boolean;
  edgeBleed?: boolean;
}) {
  return (
    <div
      className={cn(
        "no-scrollbar flex gap-2 overflow-x-auto scroll-touch py-1",
        edgeBleed && "-mx-3 px-3 sm:mx-0 sm:px-0",
        wrap && "sm:flex-wrap sm:overflow-visible",
        className,
      )}
    >
      {children}
    </div>
  );
}

/**
 * Standard page-section header. Use this for every list/section title across
 * the app: object name in monospaced uppercase, count chip, then right-justified
 * action buttons (use `<HeaderAction>` for those).
 *
 *   <PageSectionHeader title="skills" count={items.length}>
 *     <HeaderAction icon={<Plus />} label="New cron" onClick={…} primary />
 *     <HeaderAction icon={<RefreshCw />} label="Refresh" onClick={…} />
 *   </PageSectionHeader>
 */
export function PageSectionHeader({
  title,
  count,
  meta,
  children,
  className,
}: {
  title: string;
  count?: number | null;
  /**
   * Optional inline content rendered immediately after the title (and the
   * count chip, if any). Use this for a single status tag like
   * `every 30m` or `paused` - keep it short, it sits on the same row as
   * the action buttons on desktop.
   */
  meta?: React.ReactNode;
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center gap-2", className)}>
      <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        {title}
      </span>
      {typeof count === "number" ? (
        <Badge
          variant="secondary"
          className="h-5 min-w-[1.25rem] justify-center px-1.5 font-mono text-[10px]"
        >
          {count}
        </Badge>
      ) : null}
      {meta ? <div className="flex items-center gap-1.5">{meta}</div> : null}
      {children ? <div className="ml-auto flex items-center gap-1">{children}</div> : null}
    </div>
  );
}

/**
 * Header action button. Always ghost - no filled backgrounds anywhere.
 *   • mobile (<sm)  → square 36×36 icon button (no label, no bg)
 *   • sm+           → icon + label, still ghost (text only, no bg)
 *
 * Pass `primary` to bump the icon to `text-foreground` on a ghost surface
 * so the eye lands on it before secondary actions; we never re-introduce
 * a filled background, since stacked filled buttons read as bulky on
 * mobile and the rest of the app is ghost-styled.
 */
export const HeaderAction = React.forwardRef<
  HTMLButtonElement,
  Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
    icon: React.ReactNode;
    label: string;
    primary?: boolean;
    loading?: boolean;
  }
>(({ icon, label, primary, loading, className, ...props }, ref) => {
  return (
    <Button
      ref={ref}
      type="button"
      size="sm"
      variant="ghost"
      aria-label={label}
      title={label}
      className={cn(
        // Tight ghost icon: square on mobile, expands on sm+. No filled
        // background - so we drop to h-7 (28px) on mobile to match the
        // 11px section title's visual weight. Without that, the row
        // expands to the button's chrome and the title appears to "float"
        // away from the card's top edge. On sm+ we keep h-8 since the
        // text label needs the extra height to read comfortably.
        "h-7 w-7 shrink-0 px-0 sm:h-8 sm:w-auto sm:gap-1.5 sm:px-3",
        primary
          ? "text-foreground hover:text-foreground"
          : "text-muted-foreground hover:text-foreground",
        className,
      )}
      {...props}
    >
      <span className={cn("inline-flex", loading && "animate-spin")}>{icon}</span>
      <span className="hidden sm:inline">{label}</span>
    </Button>
  );
});
HeaderAction.displayName = "HeaderAction";

/**
 * Sub-filter pill. Same look as the memory-page tier chips. Always paired
 * with `<HScrollRow>` (or any other flex container) - never used standalone.
 */
export const FilterPill = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & { active?: boolean }
>(({ className, active, ...props }, ref) => (
  <button
    ref={ref}
    type="button"
    className={cn(
      "inline-flex h-8 shrink-0 items-center rounded-full border px-3.5 font-mono text-[11px] uppercase tracking-wider transition-colors",
      active
        ? "border-info bg-info/10 text-info"
        : "border-border bg-muted text-muted-foreground hover:bg-accent",
      className,
    )}
    {...props}
  />
));
FilterPill.displayName = "FilterPill";
