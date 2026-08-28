"use client";

import * as React from "react";
import { Loader2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { SearchInput } from "@/components/ui/search-input";
import { todayHeader } from "@/lib/dashboard/format";

/* Dashboard page header — Majordomo §5 (`PageHeader`) + §1.3 (one title).
 *
 * Lives *inside* the TabFrame's main area; the global TopBar (logo, tabs,
 * theme toggle, hamburger) is TabFrame's. This is the page-scoped header: the
 * route's ONE `<h1>` ("Jarvis"), one quiet meta line, and the page search.
 *
 * WHAT CHANGED, AND WHY
 *
 * The title, the pulsing dot, the date, and the "N need you" pill were four
 * hand-rolled pieces of chrome across two rows — a bordered brand pill next to
 * a 3xl semibold heading. All four are ONE line now: `PageHeader` renders the
 * h1 in the voice face with the `live` dot, and everything that was a badge is
 * a clause in the quiet meta line ("Thursday, August 28 · 9:40pm · 3 need
 * you"). Nothing was dropped: the count is still there, the date is still
 * there, the in-flight spinner is still there (right-aligned in `actions`).
 *
 * The search field routes through `<SearchInput>` rather than a hand-rolled
 * input + magnifier + clear button (reuse-first: that primitive already owns
 * the 16px iOS font, the inputMode, the focus ring, and the one-tap clear).
 * Search still filters everything visible on the dashboard; it is NOT the
 * eventual global cmd-K palette.
 *
 * HYDRATION: `todayHeader()` and the clock are locale/clock dependent. The date
 * carries `suppressHydrationWarning`; the clock starts EMPTY and is filled in
 * an effect, so the server and the first client render agree by construction —
 * never `Date.now()` in a `useState` initializer.
 */
export function DashboardHeader({
  badgeCount,
  search,
  onSearchChange,
  loading = false,
}: {
  badgeCount: number;
  search: string;
  onSearchChange: (v: string) => void;
  // True while the dashboard is fetching/refetching. Surfaces a small spinner
  // in the header so the boss can tell the page is in flight instead of
  // staring at empty sections wondering.
  loading?: boolean;
}) {
  const { title, sub } = todayHeader();

  // Local clock, filled after mount and refreshed each minute. Empty on the
  // server and on the first client paint, so it can never mismatch.
  const [clock, setClock] = React.useState("");
  React.useEffect(() => {
    const tick = () =>
      setClock(
        new Date()
          .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
          .toLowerCase()
          .replace(/\s/g, ""),
      );
    tick();
    const t = window.setInterval(tick, 60_000);
    return () => window.clearInterval(t);
  }, []);

  const meta = (
    <span suppressHydrationWarning>
      {title}, {sub}
      {clock ? ` · ${clock}` : ""}
      {badgeCount > 0 ? ` · ${badgeCount} need you` : ""}
    </span>
  );

  return (
    <div className="mx-auto w-full min-w-0 max-w-6xl space-y-3 px-4 pb-3 pt-1 sm:px-6 sm:pt-2">
      <PageHeader
        title="Jarvis"
        live
        meta={meta}
        actions={
          loading ? (
            <span
              className="inline-flex items-center text-quiet"
              aria-live="polite"
              aria-label="Loading dashboard"
              title="Loading dashboard"
            >
              <Loader2 className="size-4 animate-spin" aria-hidden />
            </span>
          ) : null
        }
        className="pb-0"
      />

      <SearchInput
        value={search}
        onValueChange={onSearchChange}
        placeholder="Search the dashboard…"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
      />
    </div>
  );
}
