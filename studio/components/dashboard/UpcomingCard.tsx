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

/* Upcoming - calendar feed, 6 months out, no empty weeks.
 *
 * Events are grouped by day, and any day without events is omitted
 * entirely (per the boss's request - "no empty weeks"). Long stretches
 * with no events get a single thin "nothing scheduled · 12 days" row
 * inside the scroll so the temporal gap is still legible without
 * burning vertical space.
 *
 * Each event shows a prep count badge if any prep items remain open.
 * Classification icons make event types scannable at a glance.
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

type DayBucket = { dayStart: number; events: CalendarEvent[] };
type Row = { kind: "day"; bucket: DayBucket } | { kind: "gap"; days: number };

export function UpcomingCard({
  events,
  onOpen,
}: {
  events: CalendarEvent[];
  onOpen: (item: DashboardItem) => void;
}) {
  const rows = useMemo<Row[]>(() => {
    const today = startOfDay(new Date());
    // Group by day-start for stable bucketing across the 6-month range.
    const buckets = new Map<number, CalendarEvent[]>();
    for (const e of events) {
      const k = startOfDay(e.startsAt);
      if (k < today) continue;
      const arr = buckets.get(k) ?? [];
      arr.push(e);
      buckets.set(k, arr);
    }
    const sortedDays = Array.from(buckets.entries()).sort((a, b) => a[0] - b[0]);
    // Insert "gap" rows when consecutive buckets are >1 day apart, but
    // skip gaps for very small differences (today → tomorrow is no gap).
    const out: Row[] = [];
    let prevDay: number | null = null;
    for (const [day, evs] of sortedDays) {
      if (prevDay !== null) {
        const dayDiff = Math.round((day - prevDay) / (24 * 60 * 60 * 1000)) - 1;
        if (dayDiff >= 2) {
          out.push({ kind: "gap", days: dayDiff });
        }
      }
      out.push({
        kind: "day",
        bucket: { dayStart: day, events: evs.sort((a, b) => a.startsAt.localeCompare(b.startsAt)) },
      });
      prevDay = day;
    }
    return out;
  }, [events]);

  return (
    <Section
      title="Upcoming"
      Icon={CalendarRange}
      delay={0.15}
      action={{ label: "next 6 months", href: "/cron" }}
    >
      <div className="overflow-hidden rounded-xl border bg-card">
        <div className="max-h-[460px] overflow-y-auto scroll-touch">
          <ol className="divide-y divide-border/60">
            {rows.length === 0 ? (
              <li className="px-3 py-6 text-center text-xs text-muted-foreground">
                Nothing scheduled in the next 6 months.
              </li>
            ) : (
              rows.map((row, i) => {
                if (row.kind === "gap") {
                  return (
                    <li
                      key={`gap-${i}`}
                      className="bg-muted/20 px-3 py-1.5 text-center font-mono text-[10px] uppercase tracking-wider text-muted-foreground"
                    >
                      · {row.days} {row.days === 1 ? "day" : "days"} clear ·
                    </li>
                  );
                }
                const b = row.bucket;
                const label = dayLabel(new Date(b.dayStart).toISOString());
                return (
                  <li key={`day-${b.dayStart}`}>
                    <div className="sticky top-0 z-10 flex items-baseline gap-2 bg-card/95 px-3 py-1.5 backdrop-blur supports-[backdrop-filter]:bg-card/85">
                      <span className="text-[11px] font-semibold tracking-tight text-foreground" suppressHydrationWarning>
                        {label}
                      </span>
                      <span className="font-mono text-[10px] text-muted-foreground" suppressHydrationWarning>
                        {new Date(b.dayStart).toLocaleDateString([], {
                          month: "short",
                          day: "numeric",
                        })}
                      </span>
                    </div>
                    <ul>
                      {b.events.map((e) => (
                        <EventRow
                          key={e.id}
                          e={e}
                          onClick={() => onOpen({ kind: "event", data: e })}
                        />
                      ))}
                    </ul>
                  </li>
                );
              })
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
  return (
    <li>
      <motion.button
        type="button"
        onClick={onClick}
        whileHover={{ x: 2 }}
        transition={{ duration: 0.12 }}
        className="flex w-full items-start gap-2.5 px-3 py-2 text-left transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
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
            <span
              className="font-mono text-[11px] text-muted-foreground"
              suppressHydrationWarning
            >
              {clockTime(e.startsAt)}
            </span>
            <span className="truncate text-sm font-medium text-foreground">{e.title}</span>
          </div>
          {e.location ? (
            <p className="mt-0.5 flex items-center gap-1 truncate text-[11px] text-muted-foreground">
              <MapPin className="size-3 shrink-0" aria-hidden />
              {e.location}
            </p>
          ) : null}
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
