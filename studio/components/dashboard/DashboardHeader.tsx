"use client";

import * as React from "react";
import { PageHeader } from "@/components/ui/page-header";
import { SearchInput } from "@/components/ui/search-input";
import { DailyQuote } from "@/components/dashboard/DailyQuote";
import { todayHeader } from "@/lib/dashboard/format";
import type { DailyQuote as DailyQuoteData } from "@/lib/dashboard/types";

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
 * It runs the SAME global search as the ⌘K palette (GET /api/search) — it used
 * to only filter the rows already on screen, which meant the one box on the
 * page he would reach for to find something could not find anything he could
 * not already see. Results replace the sections; DashboardClient owns that
 * swap.
 *
 * The greeting is set in the display register and says his name. It is the
 * only place in the product where somebody is being spoken to rather than a
 * surface being labelled, which is the entire justification for a second
 * typeface existing at all (MAJORDOMO §4, amended 2026-08-30). The name comes
 * down inside the dashboard payload from the boss profile — never a literal in
 * this file, and absent it the greeting is simply nameless.
 *
 * HYDRATION: `todayHeader()` and the clock are locale/clock dependent. The date
 * carries `suppressHydrationWarning`; the clock starts EMPTY and is filled in
 * an effect, so the server and the first client render agree by construction —
 * never `Date.now()` in a `useState` initializer.
 */
export function DashboardHeader({
  bossName,
  quote,
  search,
  onSearchChange,
}: {
  /** From the boss profile via /api/dashboard. Empty until he has told us. */
  bossName?: string;
  /** Today's line. Undefined on a cold cache; the block then renders nothing. */
  quote?: DailyQuoteData | null;
  search: string;
  onSearchChange: (v: string) => void;
}) {
  // todayHeader still supplies the day for screen readers and for the
  // greeting's boundary; it is no longer printed as a middot-joined line.
  const { title } = todayHeader();
  void title;

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

  return (
    // max-w-board, the token, same as <main> and the footer. The page used to
    // state its column width three times in three different numbers, so the
    // greeting sat 44px inboard of the cards it was introducing.
    // No `space-y-*` here, deliberately. It set every gap in this block to the
    // same 12px - title to quote, quote to search, search to the first row -
    // so nothing read as belonging to anything else and the whole thing felt
    // crammed against the page. The spacing is explicit per element now and it
    // GROUPS: the quote sits tight under the greeting because they are one
    // thought, then a real break before the search, then the biggest break of
    // the three where the header hands over to the page.
    <div className="mx-auto w-full min-w-0 max-w-board px-4 pb-8 pt-3 sm:px-6 sm:pb-12 sm:pt-5">
      <PageHeader
        title={greeting(clock, bossName)}
        titleFace="display"
        live
        // No `meta`. The line under the greeting used to be "Nothing needs
        // you right now.", which said nothing on the good days and duplicated
        // the count already sitting on the "Needs you" section on the bad
        // ones. The quote holds that slot instead — as a SIBLING below, not
        // through `meta`: meta renders inside a <p>, and a <figure> inside a
        // <p> is invalid, so the parser closes the <p> early and the server
        // and client disagree about the DOM.
        className="pb-0"
      />

      <DailyQuote quote={quote} className="mt-2" />

      {/* The margin goes on a wrapper, not on SearchInput's className: that
          prop lands on the <input>, and the magnifier is absolutely centred
          against the wrapper, so a margin there would drop the field away
          from its own icon. */}
      <div className="mt-6 sm:mt-7">
        <SearchInput
          value={search}
          onValueChange={onSearchChange}
          placeholder="Search everything…"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
        />
      </div>
    </div>
  );
}

/**
 * "Good morning, Kai" — derived from the same client clock the header already
 * keeps, so it can never mismatch between server and client. Empty clock
 * (first paint) falls back to the neutral form rather than guessing at a time
 * of day.
 *
 * A missing name yields "Good evening" with no trailing comma. That is the
 * correct output, not a degraded one: a greeting with no name reads fine, a
 * greeting with a guessed name reads wrong.
 */
function greeting(clock: string, name?: string): string {
  const timeOfDay = (() => {
    if (!clock) return "Hello";
    const hour = parseInt(clock, 10);
    const pm = clock.includes("pm");
    const h24 = pm ? (hour === 12 ? 12 : hour + 12) : hour === 12 ? 0 : hour;
    if (h24 < 12) return "Good morning";
    if (h24 < 18) return "Good afternoon";
    return "Good evening";
  })();
  const who = name?.trim();
  return who ? `${timeOfDay}, ${who}` : timeOfDay;
}
