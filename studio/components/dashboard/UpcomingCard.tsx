"use client";

import { useMemo } from "react";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { clockTime, dayLabel, eventDate, startOfDay } from "@/lib/dashboard/format";
import type { CalendarEvent, DashboardItem } from "@/lib/dashboard/types";

/* Calendar - what is coming up, out to six months.
 *
 * MAJORDOMO SWEEP. Three things changed and nothing was dropped:
 *
 *  - The month heading was a sticky `bg-card` strip that only worked because
 *    the section was a card; it is a `GroupLabel` now (mono, uppercase, quiet)
 *    - the same label the Agent Work board and every other grouped list uses.
 *  - The `size-8 rounded-md border` date tile is a bare mono day number in the
 *    row's leading slot. A number does not need a box to be a date.
 *  - The chip row (account · rsvp · conference · recurring) and the local
 *    hand-rolled `Chip` component that duplicated `./Chip` are gone; every one
 *    of those facts is now a clause in the row's quiet meta line, which is
 *    where a fact belongs when it is not interactive (§7).
 *
 * An event with unfinished prep still reads as needing him: the row's tone dot
 * turns amber and the open-prep count sits on the right.
 */

type Row =
  | { kind: "month"; label: string }
  | { kind: "event"; event: CalendarEvent };

/** Rows kept visible before the list scrolls internally. Higher than the
 *  shared card cap because month labels take slots without being events. */
const CALENDAR_ROWS = 7;

export function UpcomingCard({
  events,
  onOpen,
  matchHeight,
}: {
  events: CalendarEvent[];
  onOpen: (item: DashboardItem) => void;
  /** Legacy explicit pixel cap (ScrollList "matched" mode). No longer threaded
   *  by the dashboard - see `./listHeight` for why. */
  matchHeight?: number | null;
}) {
  const rows = useMemo<Row[]>(() => {
    const today = startOfDay(new Date());
    const future = events
      .filter((e) => startOfDay(eventDate(e.startsAt, e.allDay)) >= today)
      .sort((a, b) => a.startsAt.localeCompare(b.startsAt));
    const out: Row[] = [];
    let lastMonth = "";
    for (const e of future) {
      const d = eventDate(e.startsAt, e.allDay);
      // Stable monthKey so we never re-render the same header twice
      // even across timezone boundaries. Compare via getFullYear() +
      // getMonth() rather than locale string.
      const key = `${d.getFullYear()}-${d.getMonth()}`;
      if (key !== lastMonth) {
        out.push({
          kind: "month",
          label: d.toLocaleDateString([], { month: "long", year: "numeric" }),
        });
        lastMonth = key;
      }
      out.push({ kind: "event", event: e });
    }
    return out;
  }, [events]);

  return (
    <Section title="Calendar" action={{ label: "next 6 months", href: "/cron" }}>
      {rows.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">Nothing scheduled in the next 6 months.</p>
      ) : (
        <ScrollList max={CALENDAR_ROWS} maxHeight={matchHeight ?? undefined}>
          <div className="flex min-w-0 flex-col">
            {rows.map((row, i) =>
              row.kind === "month" ? (
                <GroupLabel key={`m-${row.label}-${i}`} label={row.label} />
              ) : (
                <EventRow
                  key={row.event.id}
                  e={row.event}
                  onClick={() => onOpen({ kind: "event", data: row.event })}
                />
              ),
            )}
          </div>
        </ScrollList>
      )}
    </Section>
  );
}

function EventRow({ e, onClick }: { e: CalendarEvent; onClick: () => void }) {
  // Day-of-month for the leading glyph (e.g. "13" for May 13th). All-day
  // events resolve by calendar date so a June 6 birthday reads "6", not "5".
  const day = eventDate(e.startsAt, e.allDay).getDate();
  const openPrep = (e.prep ?? []).filter((p) => !p.done).length;
  // Conference / video provider, previously a 'meet' / 'zoom' chip.
  const videoSolution = (e.conference?.solutionName ?? (e.hangoutLink ? "Meet" : "")).trim();
  // RSVP: only meaningful for invitees (events with a self responseStatus).
  const rsvp = e.responseStatus ? rsvpLabel(e.responseStatus) : "";
  const isRecurring = Array.isArray(e.recurrence) && e.recurrence.length > 0;

  const meta = [
    dayLabel(e.startsAt, e.allDay),
    e.allDay ? "all day" : clockTime(e.startsAt),
    e.location,
    e.accountLabel,
    rsvp,
    videoSolution,
    isRecurring ? "recurring" : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <ListRow
      tone={openPrep > 0 ? "warning" : "quiet"}
      leading={
        <span className="font-mono text-[12px] tabular-nums text-quiet" suppressHydrationWarning>
          {day}
        </span>
      }
      title={e.title}
      meta={<span suppressHydrationWarning>{meta}</span>}
      trailing={
        openPrep > 0 ? (
          <span
            className="shrink-0 font-mono text-[11px] tabular-nums text-warning"
            aria-label={`${openPrep} prep items open`}
          >
            {openPrep} prep
          </span>
        ) : null
      }
      onClick={onClick}
    />
  );
}

function rsvpLabel(s: string): string {
  if (s === "accepted") return "going";
  if (s === "tentative") return "maybe";
  if (s === "declined") return "declined";
  if (s === "needsAction") return "rsvp";
  return s;
}
