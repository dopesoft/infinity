"use client";

import { motion } from "framer-motion";
import { ArrowRight, Brain, Flame } from "lucide-react";
import Link from "next/link";
import type { MemoryStats } from "@/lib/dashboard/types";

/* Memory footer - quiet telemetry showing the brain is alive.
 *
 * Subtle stat strip pinned at the bottom of the dashboard with day-level
 * memory growth numbers. Tap to jump into /memory. Visual goal: "calm
 * status bar" not "celebratory dashboard widget."
 */
export function MemoryFooter({ stats }: { stats: MemoryStats }) {
  return (
    <motion.aside
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.4, delay: 0.55 }}
      className="mx-auto w-full max-w-6xl px-3 pb-4 pt-2 sm:px-4 sm:pb-6"
    >
      <Link
        href="/memory"
        className="group flex items-center justify-between gap-3 rounded-lg border bg-card/50 px-3 py-2.5 text-[11px] transition-colors hover:bg-card hover:border-foreground/20"
      >
        <div className="flex min-w-0 items-center gap-2.5">
          <Brain className="size-3.5 shrink-0 text-tier-semantic" aria-hidden />
          <span className="font-mono uppercase tracking-wider text-muted-foreground">
            memory
          </span>
          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
            <Stat value={`+${stats.newToday}`} label="today" />
            <Sep />
            <Stat value={stats.promotedToday} label="promoted" />
            <Sep />
            <Stat value={stats.procedural} label="procedural" />
            <Sep />
            <span className="inline-flex items-center gap-0.5 text-foreground">
              <Flame className="size-3 text-rose-400" aria-hidden />
              <span className="font-mono font-semibold">{stats.streakDays}d</span>
              <span className="text-muted-foreground">streak</span>
            </span>
          </div>
        </div>
        <ArrowRight
          className="size-3 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5"
          aria-hidden
        />
      </Link>
    </motion.aside>
  );
}

function Stat({ value, label }: { value: number | string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1">
      <span className="font-mono font-semibold text-foreground">{value}</span>
      <span className="text-muted-foreground">{label}</span>
    </span>
  );
}

function Sep() {
  return <span className="text-muted-foreground/40">·</span>;
}
