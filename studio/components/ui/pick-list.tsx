"use client";

import * as React from "react";
import { ChevronRight } from "lucide-react";
import {
  ResponsiveModal,
} from "@/components/ui/responsive-modal";
import { useIsDesktop } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

/**
 * PickList — the "pick on the left, read on the right" shape.
 *
 * WHEN THIS IS THE RIGHT SHAPE
 *
 * A catalog you MOVE THROUGH: forty skills, ten settings sections. You
 * browse, open one, read it, act, open the next. A board would make you open
 * and shut forty sheets to do that, and a long single-column list would make
 * you lose your place on every return.
 *
 * The left rail is also the ONE place a mono group label is correct, because
 * everything in it is the same kind of thing and the groups are its STATE
 * ("Waiting on you", "Not working", "In use"). Elsewhere a group label is too
 * weak to separate two kinds; here it separates states of one kind.
 *
 * What is deliberately NOT on a row here: no risk chip, no last-run, no
 * description. A row gets a name and, where it earns its place, one number.
 * Everything else is in the detail, which is right there.
 *
 * INLINE STYLE, deliberately: the rail width is a prop, so the grid template
 * is a computed value rather than a styling decision (the same sanctioned
 * exception as the Composer's calculated textarea height). Everything else
 * here is a token class.
 *
 * MOBILE: below `lg` the rail IS the page and picking opens the detail in a
 * <ResponsiveModal> (a bottom sheet), so you never lose your place and back
 * is one swipe. That switch lives here, not in any consumer — a
 * `useIsDesktop() ? Dialog : Drawer` in a page file is the anti-pattern this
 * primitive exists to prevent.
 */

export function PickList({
  /** The rail: search box, groups, items. */
  list,
  /** The detail for whatever is currently picked. */
  detail,
  /** True when something is picked. Drives the mobile sheet. */
  open,
  onOpenChange,
  /** Sheet title on mobile. Keep it the picked item's name. */
  title,
  /** One quiet line under the sheet title. */
  description,
  railWidth = "14rem",
  className,
}: {
  list: React.ReactNode;
  detail: React.ReactNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  railWidth?: string;
  className?: string;
}) {
  const isDesktop = useIsDesktop();

  if (!isDesktop) {
    return (
      <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}>
        {list}
        <ResponsiveModal
          open={open}
          onOpenChange={onOpenChange}
          title={title}
          description={description}
          size="lg"
        >
          {detail}
        </ResponsiveModal>
      </div>
    );
  }

  return (
    <div
      className={cn("grid min-h-0 min-w-0 flex-1", className)}
      style={{ gridTemplateColumns: `${railWidth} minmax(0,1fr)` }}
    >
      <div className="flex min-h-0 min-w-0 flex-col border-r border-hairline">{list}</div>
      <div className="flex min-h-0 min-w-0 flex-col overflow-y-auto scroll-touch">{detail}</div>
    </div>
  );
}

/** The scrolling body of the rail. Put the search field above it, not in it. */
export function PickListItems({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("min-h-0 flex-1 overflow-y-auto scroll-touch px-1.5 pb-4 pt-1", className)}>
      {children}
    </div>
  );
}

/**
 * A state heading inside the rail. Carries the dot, so the rows beneath it
 * do not have to repeat it — the status belongs to the group, not to every
 * child.
 */
export function PickListGroup({
  label,
  count,
  tone,
}: {
  label: string;
  count?: number;
  tone?: "brand" | "warning" | "danger";
}) {
  const dot =
    tone === "warning"
      ? "bg-warning"
      : tone === "danger"
        ? "bg-danger"
        : tone === "brand"
          ? "bg-brand"
          : null;
  return (
    <div className="flex min-w-0 items-center gap-2 px-2 pb-1 pt-3 first:pt-1">
      {dot ? <span className={cn("size-1.5 shrink-0 rounded-full", dot)} aria-hidden /> : null}
      <span className="truncate font-mono text-[9.5px] uppercase tracking-[0.12em] text-quiet">
        {label}
      </span>
      {count !== undefined ? (
        <span className="ml-auto shrink-0 font-mono text-[10px] tabular-nums text-quiet">
          {count}
        </span>
      ) : null}
    </div>
  );
}

/**
 * A row in the rail.
 *
 * THE ACTIVE STATE IS INK AND A MARKER, NEVER A FILLED BLOCK. A `bg-accent`
 * rectangle behind the selected row is the single loudest thing on a quiet
 * page: it is a hard-edged shape competing with type for attention, and it
 * fights the whole design language, which says tone and hairlines rather
 * than boxes. Selection is shown the way a well-set page shows it — the name
 * goes to full-strength ink, everything else stays a step back, and a 2px
 * rule marks the edge. Hover still gets a wash, because a wash that appears
 * under your cursor and leaves again is feedback; one that sits there
 * permanently is furniture.
 */
export function PickListItem({
  label,
  /** At most ONE number. Usage count, not a second description. */
  meta,
  /** Optional glyph. One size, one colour, never a coloured badge. */
  leading,
  selected,
  onSelect,
  className,
}: {
  label: string;
  meta?: string | number;
  leading?: React.ReactNode;
  selected?: boolean;
  onSelect: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "group relative flex min-h-row w-full min-w-0 items-center gap-2.5 rounded-md py-1.5 pl-3 pr-2 text-left text-[13px] transition-colors",
        selected
          ? "font-medium text-foreground"
          : "text-muted-foreground hover:bg-accent/40 hover:text-foreground",
        className,
      )}
    >
      {/* The marker. Sits in the row's left padding, so the label's left edge
          is identical whether or not the row is selected — nothing shifts
          sideways as you move down the rail. */}
      <span
        aria-hidden
        className={cn(
          "absolute inset-y-1 left-0 w-[2px] rounded-full transition-colors",
          selected ? "bg-foreground" : "bg-transparent",
        )}
      />
      {leading ? (
        <span
          /* size-4 on the SLOT, not just the glyph: the icon column has to be
             one width for every row or the labels sit on five different left
             edges and the rail reads crooked. The primitive guarantees it
             rather than trusting each caller to pass a same-sized icon. */
          className={cn(
            "grid size-4 shrink-0 place-items-center transition-colors [&>svg]:size-4",
            selected ? "text-foreground" : "text-quiet group-hover:text-muted-foreground",
          )}
        >
          {leading}
        </span>
      ) : null}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {meta !== undefined && meta !== "" ? (
        <span className="shrink-0 font-mono text-[10px] tabular-nums text-quiet">{meta}</span>
      ) : null}
      <ChevronRight className="size-3 shrink-0 text-quiet lg:hidden" aria-hidden />
    </button>
  );
}

/** The detail pane's header: title, one context line, and nothing else. */
export function PickDetailHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 items-start gap-3 border-b border-hairline pb-3">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <h2 className="min-w-0 font-voice text-[18px] font-medium tracking-tight">{title}</h2>
        {description ? (
          <p className="min-w-0 text-[12.5px] leading-relaxed text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-1.5">{actions}</div> : null}
    </div>
  );
}
