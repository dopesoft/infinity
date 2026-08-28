"use client";

import { useMemo } from "react";
import { GroupLabel, ListRow, type RowTone } from "@/components/ui/list-row";
import { Section } from "./Section";
import { clockTime, relTime } from "@/lib/dashboard/format";
import type { ActivityEvent, ActivityKind, DashboardItem } from "@/lib/dashboard/types";

/* Activity feed - the rolling stream of agent events.
 *
 * Time-ordered, split at "now" so what is SCHEDULED reads apart from what has
 * HAPPENED. Each row taps into the ObjectViewer for the source artifact.
 *
 * MAJORDOMO SWEEP: the feed drew its own timeline - an absolutely positioned
 * 1px rail, per-row dots with a `ring-4 ring-card` halo (which only worked
 * because the section was a card), a glow shadow per tone, and a trailing
 * kind icon. That is four decorations for "something happened at a time". The
 * rows are `ListRow`s now: the tone dot is the primitive's, the "now" divider
 * is a `GroupLabel`, and the time is where every other row keeps it - in the
 * quiet meta line, tabular so the column of timestamps aligns.
 */

/** Event kind → the one alive/waiting/broken palette. An alert is the only
 *  kind that reads coloured-for-attention; the rest are grey history. */
const KIND_TONE: Record<ActivityKind, RowTone> = {
  scheduled: "quiet",
  completed: "success",
  alert: "warning",
  memory: "info",
  reflection: "info",
  system: "quiet", // folded-in operational agent notes
};

export function ActivityCard({
  activity,
  onOpen,
}: {
  activity: ActivityEvent[];
  onOpen: (item: DashboardItem) => void;
}) {
  const sorted = useMemo(
    () =>
      [...activity].sort((a, b) => {
        // Future events sorted ascending (soonest first), past descending
        // (most-recent first). The divider is rendered between the groups.
        const ta = new Date(a.at).getTime();
        const tb = new Date(b.at).getTime();
        if (a.future && b.future) return ta - tb;
        if (a.future && !b.future) return -1;
        if (!a.future && b.future) return 1;
        return tb - ta;
      }),
    [activity],
  );
  const firstPast = sorted.findIndex((e) => !e.future);

  return (
    <Section title="Activity" action={{ label: "open heartbeat", href: "/heartbeat" }}>
      {sorted.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">Nothing has happened yet today.</p>
      ) : (
        <div className="flex max-h-[420px] min-w-0 flex-col overflow-y-auto scroll-touch">
          {sorted.map((e, i) => (
            <div key={e.id} className="flex min-w-0 flex-col">
              {/* The "now" line: everything above it is still to come. */}
              {i === firstPast && i !== 0 ? <GroupLabel label="now" /> : null}
              <ListRow
                tone={KIND_TONE[e.kind]}
                title={e.title}
                meta={
                  <span suppressHydrationWarning>
                    {[e.future ? clockTime(e.at) : relTime(e.at), e.detail]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                }
                onClick={() => onOpen({ kind: "activity", data: e })}
              />
            </div>
          ))}
        </div>
      )}
    </Section>
  );
}
