import * as React from "react";
import { cn } from "@/lib/utils";

const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        "rounded-xl border bg-card text-card-foreground shadow-none",
        className,
      )}
      {...props}
    />
  ),
);
Card.displayName = "Card";

const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("flex flex-col gap-1.5 p-5", className)} {...props} />
  ),
);
CardHeader.displayName = "CardHeader";

const CardTitle = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("font-semibold leading-none tracking-tight", className)} {...props} />
  ),
);
CardTitle.displayName = "CardTitle";

const CardDescription = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("text-sm text-muted-foreground", className)} {...props} />
  ),
);
CardDescription.displayName = "CardDescription";

const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("p-5 pt-0", className)} {...props} />
  ),
);
CardContent.displayName = "CardContent";

const CardFooter = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("flex items-center gap-2 p-5 pt-0", className)} {...props} />
  ),
);
CardFooter.displayName = "CardFooter";

/**
 * SectionCard — the standard card shell for any panel that leads with a
 * <PageSectionHeader>. Bakes in the canonical padding the design system
 * uses across Heartbeat, Trust panels, future feature cards: tight top,
 * normal sides + bottom, 8px gap between children.
 *
 *   <SectionCard>
 *     <PageSectionHeader title="heartbeat" meta=… >…actions</PageSectionHeader>
 *     <div>…body</div>
 *   </SectionCard>
 *
 * Don't reach for raw <Card><CardContent className="p-4">…</CardContent></Card>
 * for header-led cards — use this so spacing stays consistent everywhere.
 */
const SectionCard = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement> & { contentClassName?: string }
>(({ className, contentClassName, children, ...props }, ref) => (
  <Card ref={ref} className={className} {...props}>
    <div
      className={cn("space-y-2 px-4 pb-4 pt-2.5", contentClassName)}
    >
      {children}
    </div>
  </Card>
));
SectionCard.displayName = "SectionCard";

export {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardFooter,
  SectionCard,
};
