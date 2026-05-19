"use client";

import { useMemo } from "react";
import { motion } from "framer-motion";
import {
  CalendarDays,
  CalendarRange,
  MapPin,
  Plane,
  Music2,
  Users,
  Utensils,
  Stethoscope,
  Briefcase,
  type LucideIcon,
} from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { clockTime, dayLabel, startOfDay } from "@/lib/dashboard/format";
import type {
  CalendarEvent,
  CalendarEventClass,
  DashboardItem,
} from "@/lib/dashboard/types";

/* Upcoming - calendar feed, 6 months out, flat list.
 *
 * No day-bucket headers, no "X days clear" gap rows - the boss wants
 * one continuous list of future events sorted by start time. Each
 * row carries its own inline date so the temporal context still
 * reads at a glance without section breaks.
 *
 * Visible height is locked to ~4 rows; the rest scroll inside the
 * card so the dashboard column never grows unbounded.
 */

const CLASS_ICON: Record<CalendarEventClass, LucideIcon> = {
  meeting: Briefcase,
  concert: Music2,
  flight: Plane,
  dinner: Utensils,
  appointment: Stethoscope,
  travel: Plane,
  social: Users,
  personal: CalendarDays,
  other: CalendarDays,
};

// Approximate height of one EventRow (px) with the typical chip row
// rendered. 4 rows × ROW_PX defines the visible viewport before
// scrolling kicks in.
const ROW_PX = 82;

type Row =
  | { kind: "month"; label: string }
  | { kind: "event"; event: CalendarEvent };

export function UpcomingCard({
  events,
  onOpen,
}: {
  events: CalendarEvent[];
  onOpen: (item: DashboardItem) => void;
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
          horizontal breathing room via their own px-4 below. */}
      <div className="overflow-hidden">
        <div
          className="overflow-y-auto scroll-touch"
          style={{ maxHeight: `${ROW_PX * 4}px` }}
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
                    className="sticky top-0 z-10 border-b border-border/60 bg-card/95 px-4 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground backdrop-blur supports-[backdrop-filter]:bg-card/85"
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
        </div>
      </div>
    </Section>
  );
}

function EventRow({ e, onClick }: { e: CalendarEvent; onClick: () => void }) {
  const Icon = CLASS_ICON[e.classification] ?? CalendarDays;
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
              : "border-border bg-muted text-muted-foreground",
          )}
        >
          <Icon className="size-3.5" aria-hidden />
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
