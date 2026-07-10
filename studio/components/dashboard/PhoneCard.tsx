"use client";

import { Phone } from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { SurfaceRow } from "./SurfaceCard";
import type { DashboardItem } from "@/lib/dashboard/types";

/* PhoneCard - Jarvis's phone line on the dashboard.
 *
 * Renders the surface='calls' group (call transcripts + missed-call cards
 * the phone monitor posts) as its own card instead of folding into
 * "Surfaced by Jarvis" - the boss asked for calls to have a first-class
 * home. Same generic contract underneath: rows are ordinary surface items
 * (SurfaceRow), tap → ObjectViewer with the full transcript. DashboardClient
 * excludes the "calls" key from the Surfaced merge so a call never renders
 * twice.
 */
export function PhoneCard({
  items,
  delay = 0.25,
  onOpen,
}: {
  items: DashboardItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
}) {
  return (
    <Section title="Phone" Icon={Phone} delay={delay} badge={items.length}>
      {items.length === 0 ? (
        <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
          No calls yet — Jarvis answers his line and logs every call here.
        </div>
      ) : (
        <ScrollList max={4}>
          <ul className="space-y-2">
            {items.map((it) => (
              <li key={`${it.kind}-${it.data.id}`}>
                {it.kind === "surface" ? (
                  <SurfaceRow item={it.data} onClick={() => onOpen(it)} />
                ) : null}
              </li>
            ))}
          </ul>
        </ScrollList>
      )}
    </Section>
  );
}
