"use client";

import * as React from "react";
import { Search, X } from "lucide-react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * SearchInput - Input wrapped with a left-side magnifier and a right-side
 * clear (X) button that appears when there's text. One-tap clear without
 * having to drag-select and delete. Use this everywhere we have a
 * search/filter field instead of hand-rolling the wrapper each time.
 *
 *   <SearchInput
 *     value={q}
 *     onValueChange={setQ}
 *     placeholder="Search files…"
 *   />
 */
type Props = Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  "value" | "onChange"
> & {
  value: string;
  onValueChange: (v: string) => void;
  /** Custom slot for the leading icon. Defaults to the Search magnifier. */
  leadingIcon?: React.ReactNode;
  /** Hides the clear button. Default false. */
  hideClear?: boolean;
  className?: string;
};

export const SearchInput = React.forwardRef<HTMLInputElement, Props>(
  ({ value, onValueChange, placeholder, leadingIcon, hideClear, className, ...rest }, ref) => {
    const innerRef = React.useRef<HTMLInputElement | null>(null);
    React.useImperativeHandle(ref, () => innerRef.current as HTMLInputElement);

    return (
      <div className="relative w-full">
        <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground">
          {leadingIcon ?? <Search className="size-4" aria-hidden />}
        </span>
        <Input
          ref={innerRef}
          value={value}
          onChange={(e) => onValueChange(e.target.value)}
          placeholder={placeholder}
          inputMode="search"
          enterKeyHint="search"
          className={cn("pl-9", value && !hideClear && "pr-9", className)}
          {...rest}
        />
        {value && !hideClear && (
          <button
            type="button"
            onMouseDown={(e) => {
              // Prevent focus loss from the input so screen-reader users
              // and keyboard users stay in the field after clearing.
              e.preventDefault();
            }}
            onClick={() => {
              onValueChange("");
              innerRef.current?.focus();
            }}
            aria-label="Clear search"
            title="Clear"
            className="absolute right-1 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" aria-hidden />
          </button>
        )}
      </div>
    );
  },
);
SearchInput.displayName = "SearchInput";
