"use client";

import * as React from "react";
import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import { useIsDesktop } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";

/**
 * SearchPage — the shape for a page whose whole job is finding something.
 *
 * WHY THIS SHAPE
 *
 * Nobody browses twelve thousand facts. Memory's real question is "does he
 * know this, and where did he get it", which is a search, so the page IS the
 * field. What sits on it before you type is only the two things worth showing
 * unprompted: what he knows about you, and what he learned today.
 *
 * The counts under the field are a <CountLine>, not five more chips —
 * they are different KINDS, and a chip row would read as five buttons of
 * equal weight with the number you came for buried inside a pill.
 *
 * AUTOFOCUS is desktop-only. Focusing an input on mount on a phone pops the
 * keyboard and scroll-anchors the page into the middle of itself, which is
 * the bug already documented for mobile drawer forms. On a phone you tap the
 * field when you want it.
 */
export function SearchPage({
  query,
  onQueryChange,
  placeholder = "Ask me what I know",
  /**
   * Hide the field when a <ScopedTabs> above already owns the search. Two
   * search boxes on one page is the ambiguity the scope rule exists to
   * prevent — whichever one is nearest the results is the real one.
   */
  hideField = false,
  /** Rendered directly under the field: a <CountLine>, usually. */
  counts,
  className,
  children,
}: {
  query: string;
  onQueryChange: (q: string) => void;
  placeholder?: string;
  hideField?: boolean;
  counts?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  const isDesktop = useIsDesktop();
  const ref = React.useRef<HTMLInputElement | null>(null);

  React.useEffect(() => {
    if (isDesktop && !hideField) ref.current?.focus();
  }, [isDesktop, hideField]);

  // `/` focuses the field, the same way ⌘K opens the palette. Ignored while
  // you are already typing somewhere, so it can never steal a keystroke.
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/") return;
      const el = e.target as HTMLElement | null;
      const tag = el?.tagName?.toLowerCase();
      if (tag === "input" || tag === "textarea" || el?.isContentEditable) return;
      e.preventDefault();
      ref.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className={cn("flex min-w-0 flex-col gap-3.5", className)}>
      {hideField ? null : (
      <div className="relative min-w-0">
        <Search
          className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-quiet"
          aria-hidden
        />
        <Input
          ref={ref}
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          placeholder={placeholder}
          inputMode="search"
          enterKeyHint="search"
          aria-label={placeholder}
          className="h-11 rounded-[11px] pl-11 text-[15px]"
        />
        <kbd className="pointer-events-none absolute right-3 top-1/2 hidden -translate-y-1/2 rounded border border-border px-1.5 py-0.5 font-mono text-[10px] text-quiet lg:block">
          /
        </kbd>
      </div>
      )}
      {counts}
      {children}
    </div>
  );
}
