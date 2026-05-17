"use client";

import * as React from "react";
import { motion } from "framer-motion";
import { ArrowRight, type LucideIcon } from "lucide-react";
import Link from "next/link";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";

/* Section + TileCard - the universal chrome every dashboard card uses.
 *
 * Sweep 2026-05-16 (Untitled-UI style pass):
 *
 *  - Section is a real rounded-2xl card surface (no header bg strip,
 *    no fake "mail app" toolbar). The header is just the title row
 *    with whitespace below; a thin divider separates header from body.
 *  - Title typography goes calm: text-sm font-semibold tracking-tight,
 *    NOT all-caps eyebrow. The hierarchy comes from weight + spacing.
 *  - Count badges are pill-shaped (rounded-full) with a thin border,
 *    same shape as the recipient chips in the reference shots.
 *  - "See all" link reads as a small ghost button so it feels tappable
 *    without competing with the title.
 *  - Body padding scales: p-4 mobile, p-5 desktop. No more cramped lists.
 *
 * TileCard:
 *  - Single neutral border, rounded-xl, generous padding. No colored
 *    left-accent bars per tone - the content does the talking.
 *  - Hover = subtle lift + brand-tinted ring, focus = brand ring. No
 *    splashy backgrounds.
 *  - `tone` still exists but only nudges the border on hover; rest at
 *    rest stays the same neutral so the page reads calm.
 */
export function Section({
  title,
  Icon,
  badge,
  action,
  delay = 0,
  className,
  contentClassName,
  noPad,
  children,
}: {
  title: string;
  Icon?: LucideIcon;
  badge?: number | string;
  action?: { label: string; href: string };
  delay?: number;
  className?: string;
  contentClassName?: string;
  /** Skip the default body padding when the children bring their own. */
  noPad?: boolean;
  children: React.ReactNode;
}) {
  const hasBadge =
    badge !== undefined && badge !== null && badge !== 0 && badge !== "";
  return (
    <motion.section
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, delay, ease: [0.2, 0.7, 0.2, 1] }}
      className={cn(
        "min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card text-card-foreground",
        className,
      )}
    >
      <header className="flex items-center justify-between gap-3 px-4 pb-3 pt-4 sm:px-5">
        <div className="flex min-w-0 items-center gap-2">
          {Icon ? (
            <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          ) : null}
          <h2 className="truncate text-sm font-semibold tracking-tight text-foreground">
            {title}
          </h2>
          {hasBadge ? (
            <Badge
              variant="outline"
              className="h-5 min-w-[22px] justify-center rounded-full border-border bg-background px-1.5 font-mono text-[10px] font-medium tabular-nums text-muted-foreground"
            >
              {badge}
            </Badge>
          ) : null}
        </div>
        {action ? (
          <Link
            href={action.href}
            className="group inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            {action.label}
            <ArrowRight
              className="size-3 transition-transform group-hover:translate-x-0.5"
              aria-hidden
            />
          </Link>
        ) : null}
      </header>
      <div
        className={cn(
          "border-t",
          noPad ? "" : "p-4 sm:p-5",
          contentClassName,
        )}
      >
        {children}
      </div>
    </motion.section>
  );
}

export const TileCard = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & {
    tone?: "default" | "accent" | "warning" | "info" | "success" | "danger";
  }
>(({ className, tone = "default", ...props }, ref) => {
  // Tone only colors the hover border; rest-state is uniformly neutral
  // so the page reads as one calm surface instead of a rainbow.
  const hoverBorder = {
    default: "hover:border-foreground/40",
    accent: "hover:border-brand/60",
    warning: "hover:border-warning/60",
    info: "hover:border-info/60",
    success: "hover:border-brand/60",
    danger: "hover:border-danger/60",
  }[tone];
  return (
    <button
      ref={ref}
      type="button"
      className={cn(
        "group relative flex w-full min-w-0 max-w-full items-center gap-3 overflow-hidden rounded-xl border border-border bg-background p-4 text-left transition-all duration-200",
        "hover:-translate-y-px hover:shadow-sm",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 focus-visible:ring-offset-2 ring-offset-background",
        "active:translate-y-0",
        hoverBorder,
        className,
      )}
      {...props}
    />
  );
});
TileCard.displayName = "TileCard";
