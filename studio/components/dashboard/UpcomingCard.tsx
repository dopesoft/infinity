"use client";

import { useMemo } from "react";
import { motion } from "framer-motion";
import { CalendarRange, MapPin } from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, startOfDay } from "@/lib/dashboard/format";
import type { CalendarEvent, DashboardItem } from "@/lib/dashboard/types";

type Row =
  | { kind: "month"; label: string }
  | { kind: "event"; event: CalendarEvent };

// The Follow-ups card body is padded (p-5 → 20px above its ScrollList on
// lg); Upcoming uses `noPad` (0px above its ScrollList) so the month
// header band can span edge-to-edge. To make the two fade lines land on
// the SAME y, Upcoming clips 20px lower than Follow-ups' measured height,
// cancelling that top-offset difference. Desktop-only concern — the cards
// stack on mobile where alignment is moot.
const NOPAD_TOP_OFFSET = 20;

export function UpcomingCard({
  events,
  onOpen,
  matchHeight,
}: {
  events: CalendarEvent[];
  onOpen: (item: DashboardItem) => void;
  /**
   * Pixel height of the Follow-ups card's scroll region (its measured
   * 4-row clip height). When provided, Upcoming clips at the matching
   * line so both fades sit level and the shorter event rows fill the
   * space with ~6 events + scroll instead of 3 + dead space. Falls back
   * to a row-count cap before the sibling has measured / on mobile.
   */
  matchHeight?: number | null;
}) {
  const rows = useMemo<Row[]>(() => {
    const today = startOfDay(new Date());
    const future = events
      .filter((e) => startOfDay(e.startsAt) >= today)
      .sort((a, b) => a.startsAt.localeCompare(b.startsAt));
    const out: Row[] = [];
    let lastMonth = "";
    for (const e of future) {
      const d = new Date(e.startsAt);
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
    <Section
      title="Upcoming"
      Icon={CalendarRange}
      delay={0.15}
      action={{ label: "next 6 months", href: "/cron" }}
      noPad
    >
      {/* noPad on Section means the month header background spans the
          full card width edge-to-edge. Individual event rows still get
          horizontal breathing room via their own px-4 below.
          Matched mode: clip to the Follow-ups card's measured height
          (+ the noPad offset) so the fade lines align and the short event
          rows fill the space. Before the sibling has reported a height
          (first paint / mobile), fall back to a generous row count so the
          card still shows several events rather than 3. */}
      <ScrollList
        max={7}
        maxHeight={matchHeight != null ? matchHeight + NOPAD_TOP_OFFSET : undefined}
      >
        <ol className="divide-y divide-border/60">
          {rows.length === 0 ? (
            <li className="px-4 py-6 text-center text-xs text-muted-foreground">
              Nothing scheduled in the next 6 months.
            </li>
          ) : (
            rows.map((row, i) =>
              row.kind === "month" ? (
                <li
                  key={`m-${row.label}-${i}`}
                  className="sticky -top-px z-10 bg-card px-4 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground"
                >
                  {row.label}
                </li>
              ) : (
                <EventRow
                  key={row.event.id}
                  e={row.event}
                  onClick={() => onOpen({ kind: "event", data: row.event })}
                />
              ),
            )
          )}
        </ol>
      </ScrollList>
    </Section>
  );
}

function EventRow({ e, onClick }: { e: CalendarEvent; onClick: () => void }) {
  // Day-of-month for the date tile (e.g. "13" for May 13th).
  const day = new Date(e.startsAt).getDate();
  const openPrep = e.prep.filter((p) => !p.done).length;
  // Conference / video provider for the optional 'meet' / 'zoom' chip.
  const videoSolution = (
    e.conference?.solutionName ??
    (e.hangoutLink ? "Meet" : "")
  ).trim();
  // RSVP chip: only render for invitees (events with a self
  // responseStatus). Organizer events have null/undefined.
  const rsvp = e.responseStatus;
  // Recurrence chip: any non-empty recurrence array → "recurring".
  const isRecurring = Array.isArray(e.recurrence) && e.recurrence.length > 0;

  return (
    <li>
      <motion.button
        type="button"
        onClick={onClick}
        whileHover={{ x: 2 }}
        transition={{ duration: 0.12 }}
        className="flex w-full items-start gap-2.5 px-4 py-2 text-left transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
      >
        <span
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-md border",
            openPrep > 0
              ? "border-rose-400/40 bg-rose-400/10 text-rose-400"
              : "border-border bg-muted text-foreground/80",
          )}
        >
          <span className="text-sm font-semibold tabular-nums leading-none" suppressHydrationWarning>
            {day}
          </span>
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="truncate text-sm font-medium text-foreground">{e.title}</span>
          </div>
          <p
            className="mt-0.5 flex items-center gap-1.5 truncate text-[11px] text-muted-foreground"
            suppressHydrationWarning
          >
            <span className="font-medium text-foreground/80">{dayLabel(e.startsAt)}</span>
            <span className="font-mono">{clockTime(e.startsAt)}</span>
            {e.location ? (
              <>
                <span aria-hidden>·</span>
                <MapPin className="size-3 shrink-0" aria-hidden />
                <span className="truncate">{e.location}</span>
              </>
            ) : null}
          </p>
          {/* Chip row - account first (which calendar), then derived
              attribution (rsvp state, conference provider, recurring,
              classification). Same chip primitive shape as FollowUps. */}
          {(e.accountLabel || rsvp || videoSolution || isRecurring) && (
            <div className="mt-1.5 flex flex-wrap items-center gap-1">
              {e.accountLabel ? <Chip tone="muted">{e.accountLabel}</Chip> : null}
              {rsvp ? <Chip tone={rsvpTone(rsvp)}>{rsvpLabel(rsvp)}</Chip> : null}
              {videoSolution ? <Chip tone="info">{videoSolution}</Chip> : null}
              {isRecurring ? <Chip tone="muted">recurring</Chip> : null}
            </div>
          )}
        </div>
        {openPrep > 0 ? (
          <span
            aria-label={`${openPrep} prep items open`}
            className="inline-flex h-5 min-w-[20px] shrink-0 items-center justify-center rounded-full bg-rose-400/15 px-1.5 font-mono text-[10px] font-semibold text-rose-400"
          >
            {openPrep}
          </span>
        ) : null}
      </motion.button>
    </li>
  );
}

// Chip - matches the FollowUps card chip shape exactly (rounded-md,
// thin border, font-mono uppercase 10px text). Tone tints bg/text/border
// for known signals; defaults to muted neutral for free-form strings.
function Chip({
  children,
  tone = "muted",
}: {
  children: React.ReactNode;
  tone?: "muted" | "info" | "warn" | "success" | "danger";
}) {
  const cls = {
    muted: "border-border bg-muted text-muted-foreground",
    info: "border-info/30 bg-info/10 text-info",
    warn: "border-amber-500/30 bg-amber-500/10 text-amber-400",
    success: "border-emerald-500/30 bg-emerald-500/10 text-emerald-400",
    danger: "border-rose-500/30 bg-rose-500/10 text-rose-400",
  }[tone];
  return (
    <span
      className={cn(
        "inline-flex h-5 max-w-full items-center truncate rounded-md border px-1.5 font-mono text-[10px] uppercase tracking-wider",
        cls,
      )}
    >
      {children}
    </span>
  );
}

function rsvpTone(s: string): "muted" | "success" | "warn" | "danger" {
  if (s === "accepted") return "success";
  if (s === "tentative") return "warn";
  if (s === "declined") return "danger";
  return "muted";
}

function rsvpLabel(s: string): string {
  if (s === "accepted") return "going";
  if (s === "tentative") return "maybe";
  if (s === "declined") return "declined";
  if (s === "needsAction") return "rsvp";
  return s;
}
