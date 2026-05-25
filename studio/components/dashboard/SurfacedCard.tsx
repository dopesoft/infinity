"use client";

import { Sparkles } from "lucide-react";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { ApprovalRow } from "./ApprovalsCard";
import { SurfaceRow } from "./SurfaceCard";
import type { DashboardItem } from "@/lib/dashboard/types";

/* SurfacedCard - the ONE card for everything Jarvis raises to the boss.
 *
 * Merges what used to be three separate regions (Questions, Alerts, and
 * every per-key surface card) into a single importance-sorted list:
 *  - approvals  (trust_* / code_proposal / curiosity)  → ApprovalRow
 *  - surface items (alerts / insights / digest / …)     → SurfaceRow
 *
 * The merge + sort happens upstream in DashboardClient (decisions rank to
 * the top so a yes/no never hides under FYIs); this card just renders the
 * pre-built `DashboardItem[]`. A new surface key the agent invents still
 * appears automatically - it just lands in this unified card instead of a
 * card of its own. Tap any row → ObjectViewer with the full payload, where
 * Dismiss + Discuss with Jarvis live.
 */
export function SurfacedCard({
  items,
  delay = 0.25,
  onOpen,
}: {
  items: DashboardItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
}) {
  return (
    <Section
      title="Surfaced by Jarvis"
      Icon={Sparkles}
      delay={delay}
      badge={items.length}
    >
      {items.length === 0 ? (
        <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
          Nothing surfaced right now.
        </div>
      ) : (
        <ScrollList max={4}>
          <ul className="space-y-2">
            {items.map((it) => (
              <li key={`${it.kind}-${it.data.id}`}>
                {it.kind === "approval" ? (
                  <ApprovalRow a={it.data} onClick={() => onOpen(it)} />
                ) : it.kind === "surface" ? (
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
