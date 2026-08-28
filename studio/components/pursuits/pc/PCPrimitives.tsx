"use client";

import { cn } from "@/lib/utils";

/* Shared chrome for every cockpit panel.
 *
 * These exist so no panel hand-rolls its own card or empty state: the
 * overflow discipline (`min-w-0 max-w-full overflow-hidden`) and the muted
 * empty-state voice are defined once and inherited, rather than copied and
 * then quietly dropped by the next panel someone adds.
 */

export function PCCard({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section
      className={cn(
        "min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card p-4 sm:p-5",
        className,
      )}
    >
      {children}
    </section>
  );
}

export function EmptyNote({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <p className={cn("text-sm leading-relaxed text-muted-foreground", className)}>{children}</p>
  );
}
