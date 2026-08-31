"use client";

import * as React from "react";
import { SearchInput } from "@/components/ui/search-input";
import { elsewhere, scopePlaceholder, type TabCounts } from "@/lib/search/scope";
import { TAB_LAYOUT_PRIMARY } from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";

/**
 * ScopedTabs — tabs, then the search field, then the filter chips. One
 * component, because that vertical ORDER *is* the scope rule.
 *
 * THE LAW THIS ENFORCES
 *
 *   A search field belongs to whatever sits directly above it.
 *
 * Tabs above it means it searches that tab, and the placeholder says which
 * one out loud ("Search 12,481 facts") so the scope is never something you
 * have to infer. ⌘K remains the only other scope, and it is always
 * everything. Two scopes, never ambiguous.
 *
 * TAB VS CHIP, the distinction that keeps this honest:
 *
 *   Tab  = a different KIND of thing. Switching changes the shape of a row
 *          and what you can do to it. Two to five, each with a count.
 *   Chip = the SAME kind, fewer of them. Switching changes how many rows you
 *          see and nothing else. Which is why chips live below the field:
 *          they narrow what you searched.
 *
 * If a row means the same thing on both sides of a control, it was never a
 * tab. If you cannot act on two things the same way, it was never a chip.
 *
 * THE TRAP THIS CLOSES: a scoped search that can silently hide a match in
 * another tab. While you type, every tab shows how many matches it holds,
 * and an empty tab tells you where the thing actually is with the counts as
 * buttons. Without that, one failed search teaches you the feature is
 * useless.
 *
 * MOBILE: the strip scrolls sideways rather than wrapping to two rows, so
 * the search field never moves down the page as you type.
 */

export type ScopedTab = {
  id: string;
  /** Plain English, and the noun the placeholder will use. */
  label: string;
  /** The size of this tab's corpus, shown at rest. */
  count?: number;
};

export type ScopedChip = {
  id: string;
  label: string;
  count?: number;
  /** Amber for the one that wants something from you. Nothing else is tinted. */
  tone?: "default" | "warning";
};

export function ScopedTabs({
  tabs,
  activeTab,
  onTabChange,
  query,
  onQueryChange,
  /** Per-tab match counts for the CURRENT query. Empty when not searching. */
  matchCounts,
  chips,
  activeChip,
  onChipChange,
  className,
  children,
}: {
  tabs: ScopedTab[];
  activeTab: string;
  onTabChange: (id: string) => void;
  query: string;
  onQueryChange: (q: string) => void;
  matchCounts?: TabCounts;
  chips?: ScopedChip[];
  activeChip?: string;
  onChipChange?: (id: string) => void;
  className?: string;
  children?: React.ReactNode;
}) {
  const searching = query.trim().length > 0;
  const active = tabs.find((t) => t.id === activeTab) ?? tabs[0];
  const other = searching && matchCounts ? elsewhere(activeTab, tabs, matchCounts) : null;

  return (
    <div className={cn("flex min-w-0 flex-col gap-3", className)}>
      {/* 1 — tabs. Different kinds. */}
      {/* The house tab look, not a third one. TAB_LAYOUT_PRIMARY styles its
          children through `[&>button]:` selectors and matches aria-selected as
          well as Radix's data-state, so this hand-rolled tablist (it has to be
          hand-rolled: the search field below is scoped to it) renders
          identically to every PageTabsList in the app. */}
      <div role="tablist" aria-label="Kind" className={cn("-mx-4 px-4 sm:mx-0 sm:px-0", TAB_LAYOUT_PRIMARY)}>
        {tabs.map((tab) => {
          const on = tab.id === active?.id;
          // While searching, the tab shows how many matches IT holds, not how
          // big it is. That swap is what makes an empty tab legible.
          const n = searching ? (matchCounts?.[tab.id] ?? 0) : tab.count;
          return (
            <button
              key={tab.id}
              role="tab"
              aria-selected={on}
              onClick={() => onTabChange(tab.id)}
              // Shape and active state come from the container's layout, so
              // this carries only what is specific to a scoped tab.
              className="inline-flex items-center whitespace-nowrap text-[12px] transition-colors hover:text-foreground"
            >
              <span>{tab.label}</span>
              {n !== undefined ? (
                <span
                  className={cn("font-mono text-[10px] tabular-nums", on ? "text-foreground/60" : "text-quiet")}
                >
                  {n.toLocaleString()}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>

      {/* 2 — the field, scoped to the tab above it and saying so. */}
      <SearchInput
        value={query}
        onValueChange={onQueryChange}
        placeholder={scopePlaceholder(active?.label ?? "", searching ? undefined : active?.count)}
        className="h-9"
      />

      {/* 3 — chips. Same kind, fewer of them. */}
      {chips && chips.length > 0 ? (
        <div className="-mx-4 flex min-w-0 gap-1.5 overflow-x-auto scroll-touch no-scrollbar px-4 sm:mx-0 sm:px-0">
          {chips.map((chip) => {
            const on = chip.id === activeChip;
            return (
              <button
                key={chip.id}
                type="button"
                aria-pressed={on}
                onClick={() => onChipChange?.(chip.id)}
                className={cn(
                  "inline-flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 text-[11.5px] transition-colors",
                  on
                    ? "border-foreground bg-foreground text-background"
                    : chip.tone === "warning"
                      ? "border-warning/40 text-warning hover:bg-warning/10"
                      : "border-border text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
              >
                <span>{chip.label}</span>
                {chip.count !== undefined ? (
                  <span
                    className={cn(
                      "font-mono text-[10px] tabular-nums",
                      on ? "text-background/65" : "text-quiet",
                    )}
                  >
                    {chip.count.toLocaleString()}
                  </span>
                ) : null}
              </button>
            );
          })}
        </div>
      ) : null}

      {/* The trap, closed. */}
      {other ? (
        <div className="flex min-w-0 flex-col gap-2 rounded-[10px] bg-muted px-3.5 py-3">
          <span className="text-[12.5px]">Nothing in {active?.label} for that.</span>
          <span className="text-[11.5px] text-quiet">
            I found it in {other.hits.length === 1 ? "one other place" : `${other.hits.length} other places`}:
          </span>
          <div className="flex min-w-0 flex-wrap gap-1.5">
            {other.hits.map((h) => (
              <button
                key={h.id}
                type="button"
                onClick={() => onTabChange(h.id)}
                className="inline-flex h-7 items-center gap-1.5 rounded-full border border-border px-3 text-[11.5px] text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground"
              >
                <span>{h.label}</span>
                <span className="font-mono text-[10px] tabular-nums text-quiet">{h.count}</span>
              </button>
            ))}
          </div>
        </div>
      ) : null}

      {children}
    </div>
  );
}
