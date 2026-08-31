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
 *   • `scrollable` is the default for page-level tabs. Renders as airy
 *     chips (same look as /settings's mobile SectionPill rail) inside a
 *     horizontal-scroll row: each trigger is a self-contained pill so
 *     labels + count badges never get crushed and the swipe is native.
 *     Use this for 2+ tabs, anything with counts, anything with variable
 *     label widths. If you're unsure, use `scrollable`.
 *   • `columns={2|3}` is a niche fallback for short text-only labels that
 *     must visually fill the row (e.g. a single-purpose modal split).
 *     Prefer `scrollable` for page-level navigation — it matches the rest
 *     of the app.
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

/**
 * TWO LEVELS OF TAB, AND THEY MUST NOT LOOK ALIKE.
 *
 * A page can carry a main section switcher AND a sub-switcher inside the
 * section it lands on. Rendering both as the same chip rail put two identical
 * strips on top of each other on mobile, with nothing saying which one was
 * above the other in the hierarchy.
 *
 *   level="primary" (default)  shadcn's segmented control: one muted rounded
 *                              container, the active tab a raised pill inside
 *                              it. Scrolls sideways on mobile rather than
 *                              wrapping, so the row never becomes two rows.
 *   level="sub"                Material underline: no container at all, the
 *                              active tab is a word with a line under it,
 *                              sitting on a hairline that runs the row. Reads
 *                              as clearly subordinate at a glance.
 *
 * Use "sub" for ANY tab strip that sits inside a screen that already has a
 * tab strip above it. If there is only one level on the page, it is primary.
 *
 * Both are `[&>button]:` descendant utilities so the underlying shadcn
 * TabsTrigger stays ignorant of them, and both win over TabsTrigger's own
 * single-class rules on specificity.
 */
export const TAB_LAYOUT_PRIMARY = [
  // The shadcn container, made scrollable instead of wrapping on mobile.
  //
  // DARK MODE, and why it needs its own line. The theme is TRUE black:
  // --background is 0% and --muted is 5%, so shadcn's default of a muted strip
  // with a --background active pill inverts in the dark — the active tab comes
  // out DARKER than the strip it sits in, and the strip itself is 5% on 0%,
  // near enough invisible. So dark gets a border to define the strip, and the
  // active tab lifts to --accent (9.6%) instead of dropping to black.
  "mb-6 h-9 no-scrollbar flex w-full max-w-full snap-x snap-proximity justify-start gap-1 overflow-x-auto rounded-lg border border-border bg-muted p-1 scroll-touch",
  "sm:inline-flex sm:w-auto sm:max-w-none sm:overflow-visible",
  // Each trigger sits flat inside the container until it is the active one.
  "[&>button]:h-7 [&>button]:shrink-0 [&>button]:snap-start [&>button]:gap-1.5 [&>button]:rounded-md [&>button]:border [&>button]:border-transparent [&>button]:bg-transparent [&>button]:px-3 [&>button]:text-muted-foreground [&>button]:shadow-none",
  "[&>button[data-state=active]]:bg-background [&>button[data-state=active]]:text-foreground [&>button[data-state=active]]:shadow-sm",
  "[&>button[aria-selected=true]]:bg-background [&>button[aria-selected=true]]:text-foreground [&>button[aria-selected=true]]:shadow-sm",
  "dark:[&>button[data-state=active]]:border-border dark:[&>button[data-state=active]]:bg-accent dark:[&>button[data-state=active]]:shadow-none",
  "dark:[&>button[aria-selected=true]]:border-border dark:[&>button[aria-selected=true]]:bg-accent dark:[&>button[aria-selected=true]]:shadow-none",
].join(" ");

export const TAB_LAYOUT_SUB = [
  // No container and no rule across the row: the only line is under the tab
  // you are on. A full-width border made this read as a container, which is
  // the primary level's job, not this one's.
  "mb-6 h-auto no-scrollbar flex w-full max-w-full snap-x snap-proximity justify-start gap-5 overflow-x-auto rounded-none border-0 bg-transparent p-0 scroll-touch",
  "sm:w-auto sm:max-w-none sm:overflow-visible",
  // Sentence case at a readable size, so the register differs from the
  // primary row's mono caps as well as the shape.
  "[&>button]:h-9 [&>button]:shrink-0 [&>button]:snap-start [&>button]:gap-1.5 [&>button]:rounded-none [&>button]:border-0 [&>button]:border-b-2 [&>button]:border-transparent [&>button]:bg-transparent [&>button]:px-0.5 [&>button]:pb-2 [&>button]:font-sans [&>button]:text-[13px] [&>button]:font-medium [&>button]:normal-case [&>button]:tracking-normal [&>button]:text-muted-foreground [&>button]:shadow-none",
  "[&>button[data-state=active]]:border-foreground [&>button[data-state=active]]:bg-transparent [&>button[data-state=active]]:text-foreground [&>button[data-state=active]]:shadow-none",
  "[&>button[aria-selected=true]]:border-foreground [&>button[aria-selected=true]]:bg-transparent [&>button[aria-selected=true]]:text-foreground [&>button[aria-selected=true]]:shadow-none",
].join(" ");

export const PageTabsList = React.forwardRef<
  React.ElementRef<typeof TabsList>,
  React.ComponentPropsWithoutRef<typeof TabsList> & {
    columns?: number;
    /** Retained for every existing caller; primary is scrollable by design. */
    scrollable?: boolean;
    /** "sub" for a strip that sits inside a screen that already has one. */
    level?: "primary" | "sub";
  }
>(({ className, columns, scrollable, level = "primary", children, ...props }, ref) => {
  // Level decides the look. `columns` is a niche fallback for short text-only
  // labels that must fill the row, and it only applies to a primary strip.
  let layout: string;
  if (level === "sub") {
    layout = TAB_LAYOUT_SUB;
  } else if (columns && !scrollable) {
    layout = cn("mb-6 h-9", COLUMN_LAYOUTS[columns] ?? "inline-flex");
  } else {
    layout = TAB_LAYOUT_PRIMARY;
  }
  return (
    <TabsList ref={ref} className={cn(layout, className)} {...props}>
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
      {/* Zero is the empty state's job, not a badge's. See board.tsx. */}
      {typeof count === "number" && count !== 0 ? (
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
