"use client";

import { motion } from "framer-motion";
import { ListRow } from "@/components/ui/list-row";
import type { MemoryStats } from "@/lib/dashboard/types";

/* Memory footer - quiet telemetry showing the brain is alive.
 *
 * The day's memory growth, pinned at the bottom of the dashboard. Tap to jump
 * into /memory. Visual goal, unchanged: "calm status bar", never a celebratory
 * widget.
 *
 * MAJORDOMO SWEEP: it was a `rounded-lg border bg-card/50` strip with a brain
 * glyph, a mono "memory" eyebrow, four stat pairs separated by hand-rolled
 * dot spans, and a flame icon. Same four numbers now, as one `ListRow` on a
 * hairline - so the page ends on a rule instead of on one last box - with the
 * figures tabular (ListRow's meta already sets `tabular-nums`, which is what
 * keeps a changing count from jittering the line).
 */
export function MemoryFooter({ stats }: { stats: MemoryStats }) {
  return (
    <motion.aside
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.35, delay: 0.15 }}
      // max-w-board, the token — the same column the header and <main> use.
      className="mx-auto w-full min-w-0 max-w-board px-4 pb-4 pt-2 sm:px-6 sm:pb-6"
    >
      <ListRow
        href="/memory"
        tone="quiet"
        title="Memory"
        noRule
        meta={`+${stats.newToday} today · ${stats.promotedToday} promoted · ${stats.procedural} procedural · ${stats.streakDays}d streak`}
        className="border-t border-hairline"
      />
    </motion.aside>
  );
}
