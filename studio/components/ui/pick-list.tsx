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

export function PickListItem({
  label,
  /** At most ONE number. Usage count, not a second description. */
  meta,
  selected,
  onSelect,
  className,
}: {
  label: string;
  meta?: string | number;
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
        "flex min-h-row w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12.5px] transition-colors",
        selected ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
        className,
      )}
    >
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
