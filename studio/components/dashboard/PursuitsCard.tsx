"use client";

import { motion } from "framer-motion";
import { Check, Flame, Plus, Target } from "lucide-react";
import { Section, TileCard } from "./Section";
import { cn } from "@/lib/utils";
import type { DashboardItem, Pursuit } from "@/lib/dashboard/types";

/* Pursuits — habits + goals + objectives merged.
 *
 * Each row carries a cadence tag (daily / weekly / goal / quarterly).
 * Habits show a check button + streak; goals show a progress bar. Tap
 * any row to open the ObjectViewer with the pursuit's history.
 */
export function PursuitsCard({
  pursuits,
  onOpen,
  onToggleHabit,
}: {
  pursuits: Pursuit[];
  onOpen: (item: DashboardItem) => void;
  onToggleHabit: (id: string) => void;
}) {
  const habits = pursuits.filter((p) => p.cadence === "daily" || p.cadence === "weekly");
  const goals = pursuits.filter((p) => p.cadence === "goal" || p.cadence === "quarterly");

  return (
    <Section
      title="Pursuits"
      Icon={Target}
      delay={0.05}
      action={{ label: "manage", href: "/memory" }}
    >
      <div className="space-y-2.5 rounded-xl border bg-card p-3">
        <ul className="space-y-1">
          {habits.map((p) => (
            <HabitRow
              key={p.id}
              p={p}
              onOpen={() => onOpen({ kind: "pursuit", data: p })}
              onToggle={() => onToggleHabit(p.id)}
            />
          ))}
        </ul>

        {goals.length > 0 && (
          <>
            <div className="border-t border-dashed" />
            <ul className="space-y-2">
              {goals.map((p) => (
                <GoalRow
                  key={p.id}
                  p={p}
                  onOpen={() => onOpen({ kind: "pursuit", data: p })}
                />
              ))}
            </ul>
          </>
        )}

        <button
          type="button"
          className="mt-1 inline-flex w-full items-center justify-center gap-1 rounded-md border border-dashed border-border py-2 text-xs text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
        >
          <Plus className="size-3.5" aria-hidden />
          New pursuit
        </button>
      </div>
    </Section>
  );
}

function HabitRow({
  p,
  onOpen,
  onToggle,
}: {
  p: Pursuit;
  onOpen: () => void;
  onToggle: () => void;
}) {
  return (
    <li className="flex items-center gap-2">
      <motion.button
        type="button"
        whileTap={{ scale: 0.85 }}
        onClick={(e) => {
          e.stopPropagation();
          onToggle();
        }}
        aria-label={p.doneToday ? "Uncheck habit" : "Check habit"}
        className={cn(
          "inline-flex size-7 shrink-0 items-center justify-center rounded-full border transition-all",
          p.doneToday
            ? "border-success bg-success text-success-foreground shadow-[0_0_12px_hsl(var(--success)/0.4)]"
            : "border-border bg-background hover:border-foreground/40",
        )}
      >
        {p.doneToday ? <Check className="size-3.5" /> : null}
      </motion.button>
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-accent/50"
      >
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-sm",
            p.doneToday ? "text-muted-foreground line-through" : "text-foreground",
          )}
        >
          {p.title}
        </span>
        <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
          {p.cadence}
        </span>
        {p.streakDays !== undefined && p.streakDays > 0 ? (
          <span className="inline-flex items-center gap-0.5 rounded-full bg-rose-400/10 px-1.5 py-0.5 text-[10px] font-mono font-semibold text-rose-400">
            <Flame className="size-2.5" aria-hidden />
            {p.streakDays}d
          </span>
        ) : null}
      </button>
    </li>
  );
}

function GoalRow({ p, onOpen }: { p: Pursuit; onOpen: () => void }) {
  const pct = p.progress ? Math.min(100, Math.round((p.progress.current / p.progress.target) * 100)) : 0;
  const statusTone =
    p.status === "ahead"
      ? "text-success"
      : p.status === "slow"
        ? "text-rose-400"
        : p.status === "at_risk"
          ? "text-danger"
          : "text-muted-foreground";
  return (
    <li>
      <TileCard onClick={onOpen} className="flex-col items-stretch gap-1.5 p-3">
        <div className="flex items-center gap-2">
          <span className="flex-1 truncate text-sm font-medium text-foreground">{p.title}</span>
          <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
            {p.cadence === "quarterly" ? "Q" : p.cadence}
          </span>
        </div>
        {p.progress ? (
          <div className="space-y-1">
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <motion.div
                initial={{ width: 0 }}
                animate={{ width: `${pct}%` }}
                transition={{ duration: 0.8, ease: [0.2, 0.7, 0.2, 1], delay: 0.15 }}
                className={cn(
                  "h-full rounded-full",
                  p.status === "ahead"
                    ? "bg-success"
                    : p.status === "slow"
                      ? "bg-rose-400"
                      : p.status === "at_risk"
                        ? "bg-danger"
                        : "bg-foreground",
                )}
              />
            </div>
            <div className="flex items-center justify-between text-[11px]">
              <span className="font-mono text-muted-foreground">
                {p.progress.current}/{p.progress.target}
                {p.progress.unit ? <span className="opacity-60"> {p.progress.unit}</span> : null}
                {" · "}
                <span className="font-semibold text-foreground">{pct}%</span>
              </span>
              {p.status ? (
                <span className={cn("font-mono uppercase tracking-wider", statusTone)}>
                  {p.status.replace("_", " ")}
                </span>
              ) : null}
            </div>
          </div>
        ) : null}
      </TileCard>
    </li>
  );
}
