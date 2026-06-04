"use client";

import { UpcomingCard } from "@/components/dashboard/UpcomingCard";
import type { CalendarEvent } from "@/lib/dashboard/types";

function ev(id: string, title: string, iso: string, extra?: Partial<CalendarEvent>): CalendarEvent {
  return {
    id,
    title,
    startsAt: iso,
    classification: "social",
    prep: [],
    accountLabel: "Mr Khaya",
    responseStatus: "needsAction",
    recurrence: ["RRULE:FREQ=WEEKLY"],
    hangoutLink: "https://meet.google.com/x",
    ...extra,
  };
}

const events: CalendarEvent[] = [
  ev("1", "Weekly Family meeting", "2026-06-07T18:00:00Z"),
  ev("2", "Weekly Family meeting", "2026-06-14T18:00:00Z"),
  ev("3", "Maxwell's Birthday!", "2026-06-17T19:00:00Z"),
  ev("4", "Weekly Family meeting", "2026-06-21T18:00:00Z"),
  ev("5", "Weekly Family meeting", "2026-06-28T18:00:00Z"),
  ev("6", "Slater's Birthday", "2026-07-05T19:00:00Z"),
  ev("7", "Weekly Family meeting", "2026-07-12T18:00:00Z"),
  ev("8", "Ntsako's Birthday", "2026-07-19T19:00:00Z"),
];

export default function Scratch() {
  return (
    <div className="min-h-screen bg-background p-8">
      <div className="max-w-md">
        <UpcomingCard events={events} onOpen={() => {}} matchHeight={360} />
      </div>
    </div>
  );
}
