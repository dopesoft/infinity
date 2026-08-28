"use client";

import * as React from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * NativeSelect — the one dropdown shape for a settings/filter control.
 *
 * A real `<select>`, not a Radix listbox: on iOS it opens the system wheel,
 * which is the right control for a phone and needs no portal, no focus trap,
 * and no scroll lock. It was previously hand-rolled inside
 * `app/settings/page.tsx`; lifted here so /skills (the risk filter) and any
 * future consumer share the same chevron, height, focus ring, and popover
 * option colours instead of forking a copy (reuse-first rule).
 *
 * Sizing matches `<Input>` (h-9 desktop, 44px touch target on coarse
 * pointers via globals.css) so a select and an input can sit on the same row
 * without one being taller than the other.
 */
export interface NativeSelectProps
  extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, "onChange" | "value"> {
  value: string;
  onValueChange: (next: string) => void;
  children: React.ReactNode;
  className?: string;
}

export const NativeSelect = React.forwardRef<HTMLSelectElement, NativeSelectProps>(
  ({ value, onValueChange, children, className, disabled, ...rest }, ref) => (
    <div className="relative min-w-0 max-w-full">
      <select
        ref={ref}
        value={value}
        disabled={disabled}
        onChange={(e) => onValueChange(e.target.value)}
        className={cn(
          "h-9 min-h-9 w-full appearance-none truncate rounded-md border border-input bg-background pl-3 pr-9 text-sm",
          "ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
          "[&>option]:bg-popover [&>option]:text-popover-foreground",
          "disabled:cursor-not-allowed disabled:opacity-60",
          className,
        )}
        {...rest}
      >
        {children}
      </select>
      <ChevronDown
        className="pointer-events-none absolute right-3 top-1/2 size-4 -translate-y-1/2 text-quiet"
        aria-hidden
      />
    </div>
  ),
);
NativeSelect.displayName = "NativeSelect";
