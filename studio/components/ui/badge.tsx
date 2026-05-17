import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/* Badge - the universal chip primitive.
 *
 * Sweep 2026-05-16: added `pill` variant for the rounded-full
 * thin-border tag shape (recipients, tags, counts) and `brand` for
 * the emerald accent. Default rest-state stays calm so a page full
 * of badges doesn't read as a tag cloud. */
const badgeVariants = cva(
  "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors",
  {
    variants: {
      variant: {
        default: "border-transparent bg-primary text-primary-foreground",
        secondary: "border-transparent bg-secondary text-secondary-foreground",
        outline: "text-foreground",
        // Pill: thin neutral border, light bg, soft text. Matches the
        // recipient-chip / +4 counter shape from the reference shots.
        pill: "border-border bg-background text-foreground hover:bg-accent/50",
        brand: "border-transparent bg-brand/15 text-brand",
        info: "border-transparent bg-info/15 text-info",
        success: "border-transparent bg-success/15 text-success",
        warning: "border-transparent bg-warning/15 text-warning-foreground",
        danger: "border-transparent bg-danger/15 text-danger",
      },
    },
    defaultVariants: { variant: "default" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
