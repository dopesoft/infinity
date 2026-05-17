"use client";

import * as React from "react";
import { motion } from "framer-motion";
import { ArrowRight, type LucideIcon } from "lucide-react";
import Link from "next/link";
import { cn } from "@/lib/utils";

/* Reusable section shell for the Dashboard.
 *
 * Every section on the Dashboard uses this same chrome: small uppercase
 * eyebrow + title row on the left, optional badge + action link on the
 * right, then the body. Motion entrance is a fade + 6px y-translate
 * scoped per-section, ordered by `delay` so the page paints top-down.
 *
 * Visual decisions:
 *  - Section title is a `font-semibold tracking-tight` heading, all caps
 *    so it reads as a label rather than competing with item content.
 *  - Badge sits inline with the title, uses muted palette.
 *  - Action link is a soft "see all" affordance with a chevron.
 *  - Body padding is consistent so cards align horizontally on desktop.
 */
export function Section({
  title,
  Icon,
  badge,
  action,
  delay = 0,
  className,
  contentClassName,
  children,
}: {
  title: string;
  Icon?: LucideIcon;
  badge?: number | string;
  action?: { label: string; href: string };
  delay?: number;
  className?: string;
  contentClassName?: string;
  children: React.ReactNode;
}) {
  return (
    <motion.section
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, delay, ease: [0.2, 0.7, 0.2, 1] }}
      className={cn("min-w-0 max-w-full space-y-2.5", className)}
    >
      <header className="flex items-end justify-between gap-3">
        <div className="flex items-center gap-2">
          {Icon ? (
            <Icon className="size-3.5 text-muted-foreground" aria-hidden />
          ) : null}
          <h2 className="text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            {title}
          </h2>
          {badge !== undefined && badge !== null && badge !== 0 && badge !== "" ? (
            <span className="inline-flex h-4 min-w-[18px] items-center justify-center rounded-full bg-foreground/10 px-1 font-mono text-[10px] font-semibold leading-none text-foreground">
              {badge}
            </span>
          ) : null}
        </div>
        {action ? (
          <Link
            href={action.href}
            className="group inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground"
          >
            {action.label}
            <ArrowRight className="size-3 transition-transform group-hover:translate-x-0.5" aria-hidden />
          </Link>
        ) : null}
      </header>
      <div className={contentClassName}>{children}</div>
    </motion.section>
  );
}

/* TileCard - the standard tappable card body used across sections.
 *
 * Wraps content in a button-like surface with a subtle hover-lift and
 * focus ring. Sections compose these for their list rows so every item
 * feels uniformly "clickable to inspect."
 */
export const TileCard = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> & {
    tone?: "default" | "accent" | "warning" | "info" | "success" | "danger";
  }
>(({ className, tone = "default", ...props }, ref) => {
  const toneCls = {
    default: "border-border bg-card hover:border-foreground/25",
    accent: "border-foreground/15 bg-card hover:border-foreground/40",
    warning: "border-rose-400/40 bg-rose-400/[0.04] hover:border-rose-400/70",
    info: "border-info/30 bg-info/[0.04] hover:border-info/60",
    success: "border-success/30 bg-success/[0.04] hover:border-success/60",
    danger: "border-danger/40 bg-danger/[0.04] hover:border-danger/70",
  }[tone];
  return (
    <button
      ref={ref}
      type="button"
      className={cn(
        "group relative flex w-full min-w-0 max-w-full items-center gap-2.5 overflow-hidden rounded-lg border p-3 text-left transition-all duration-200",
        "hover:-translate-y-px hover:shadow-[0_2px_10px_-4px_hsl(var(--foreground)/0.15)]",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ring-offset-background",
        "active:translate-y-0",
        toneCls,
        className,
      )}
      {...props}
    />
  );
});
TileCard.displayName = "TileCard";
