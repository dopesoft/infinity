"use client";

import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { DASHBOARD_LIST_ROWS } from "./listHeight";
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
 * appears automatically. Tap any row → ObjectViewer with the full payload,
 * where Dismiss + Discuss with Jarvis live.
 *
 * MAJORDOMO SWEEP: the section header icon and the dashed-border empty box are
 * gone (§1.2 — never a bordered empty state), and the rows sit on hairlines as
 * direct siblings so `ListRow`'s `last:border-b-0` can actually see which row
 * is last. Wrapping each row in an `<li>` made every row the last child of its
 * own parent, which silently ate the separators.
 */
export function SurfacedCard({
  items,
  delay = 0,
  onOpen,
  matchHeight,
}: {
  items: DashboardItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
  /**
   * Optional explicit pixel cap for the list (ScrollList "matched" mode).
   * The dashboard no longer threads one: every list card in a row clips at the
   * same ROW COUNT, and Majordomo rows are a uniform height, so the columns
   * line up by layout instead of by a measured pixel handed across rows. Kept
   * for a surface that genuinely needs to match a specific pixel line.
   */
  matchHeight?: number | null;
}) {
  return (
    <Section title="Needs you" delay={delay} badge={items.length}>
      {items.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">Nothing surfaced right now.</p>
      ) : (
        <ScrollList max={DASHBOARD_LIST_ROWS} maxHeight={matchHeight ?? undefined}>
          <div className="flex min-w-0 flex-col">
            {items.map((it) =>
              it.kind === "approval" ? (
                <ApprovalRow
                  key={`${it.kind}-${it.data.id}`}
                  a={it.data}
                  onClick={() => onOpen(it)}
                />
              ) : it.kind === "surface" ? (
                <SurfaceRow
                  key={`${it.kind}-${it.data.id}`}
                  item={it.data}
                  onClick={() => onOpen(it)}
                />
              ) : null,
            )}
          </div>
        </ScrollList>
      )}
    </Section>
  );
}
