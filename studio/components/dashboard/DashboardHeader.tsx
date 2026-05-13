"use client";

import { motion } from "framer-motion";
import { Search, Sparkles, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { todayHeader } from "@/lib/dashboard/format";

/* Dashboard page header.
 *
 * Lives *inside* the TabFrame's main area — the global TopBar (logo, tabs,
 * theme toggle, mobile hamburger) is rendered by <TabFrame>. This is the
 * page-scoped header below it: the agent's name ("Jarvis") + today's date,
 * then a page-scoped search bar.
 *
 * Search filters everything visible on the dashboard (todos, follow-ups,
 * calendar events, kanban items, saved items, activity rows). Different
 * from the eventual global cmd-K command palette which spans the whole
 * product — this is just for the current page.
 */
export function DashboardHeader({
  badgeCount,
  search,
  onSearchChange,
}: {
  badgeCount: number;
  search: string;
  onSearchChange: (v: string) => void;
}) {
  const { title, sub } = todayHeader();
  return (
    <motion.header
      initial={{ opacity: 0, y: -6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.4, ease: [0.2, 0.7, 0.2, 1] }}
      className="mx-auto w-full max-w-6xl space-y-3 px-3 pb-3 pt-4 sm:px-4 sm:pt-5"
    >
      <div className="flex items-end justify-between gap-3">
        <div className="flex min-w-0 items-end gap-3">
          <div className="relative shrink-0">
            {/* Soft sparkle indicator next to the name to convey "alive" */}
            <span
              aria-hidden
              className="absolute -right-2 -top-1 size-1.5 animate-pulse rounded-full bg-success shadow-[0_0_8px_hsl(var(--success))]"
            />
            <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
              Jarvis
            </h1>
          </div>
          <div className="min-w-0 pb-1 text-[12px] text-muted-foreground sm:text-[13px]">
            <span className="font-medium text-foreground" suppressHydrationWarning>
              {title}
            </span>
            <span className="px-1.5 text-muted-foreground/50">·</span>
            <span suppressHydrationWarning>{sub}</span>
          </div>
        </div>
        {badgeCount > 0 ? (
          <div className="flex shrink-0 items-center gap-1.5 rounded-full border border-rose-400/40 bg-rose-400/10 px-2.5 py-1 text-[11px] font-medium text-rose-400">
            <Sparkles className="size-3" aria-hidden />
            <span className="font-mono">{badgeCount}</span>
            <span className="hidden sm:inline">need you</span>
          </div>
        ) : null}
      </div>

      <div className="relative">
        <Search
          className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden
        />
        <input
          type="text"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search the dashboard…"
          inputMode="search"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          className={cn(
            "h-11 w-full rounded-lg border border-input bg-card pl-10 pr-10 text-sm",
            "transition-colors focus:border-foreground/30 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 ring-offset-background",
          )}
        />
        {search ? (
          <button
            type="button"
            onClick={() => onSearchChange("")}
            aria-label="Clear search"
            className="absolute right-2 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" />
          </button>
        ) : null}
      </div>
    </motion.header>
  );
}
