"use client";

import { motion } from "framer-motion";
import { Brain, Check, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { GroupLabel, ListRow, WorkRow, type RowTone } from "@/components/ui/list-row";
import { BoardCard } from "@/components/ui/board";
import { cn } from "@/lib/utils";
import type { DashboardItem, Pursuit } from "@/lib/dashboard/types";

/* Pursuits - habits + goals + objectives merged.
 *
 * Habits carry a tick and a streak; goals carry a progress bar. Tap any row to
 * open the ObjectViewer with the pursuit's history.
 *
 * MAJORDOMO SWEEP: the two lists were separated by a dashed rule and the goals
 * were `TileCard`s with a hand-rolled progress bar, a hand-rolled percentage
 * line, and a hand-rolled status word - a third copy of a bar that already
 * exists in the `WorkRow` primitive. Habits and goals are now `GroupLabel` +
 * rows, the goal bar IS `WorkRow`'s bar (which now takes its colour from the
 * row tone, so an at-risk goal reads red instead of emerald), and the streak
 * flame pill is a word in the meta line.
 */

/** A goal's status maps to the one alive/waiting/broken palette (§1.4). */
function statusTone(status: Pursuit["status"]): RowTone {
  if (status === "ahead") return "success";
  if (status === "slow") return "warning";
  if (status === "at_risk") return "danger";
  return "default";
}

function isCoached(p: Pursuit): boolean {
  return !!p.experience && p.experience !== "ordinary";
}

export function PursuitsCard({
  pursuits,
  onOpen,
  onToggleHabit,
}: {
  pursuits: Pursuit[];
  onOpen: (item: DashboardItem) => void;
  onToggleHabit: (id: string) => void;
}) {
  // A pursuit with a bespoke experience is neither a habit nor a goal: it is a
  // workspace you open. It gets the same row treatment as any other coached
  // pursuit regardless of its cadence, because a progress bar would describe a
  // thing the cockpit, not this card, is actually tracking.
  const habits = pursuits.filter(
    (p) => isCoached(p) || p.cadence === "daily" || p.cadence === "weekly",
  );
  const goals = pursuits.filter(
    (p) => !isCoached(p) && (p.cadence === "goal" || p.cadence === "quarterly"),
  );

  return (
    <BoardCard
      title="Pursuits"
      count={habits.length + goals.length}
      href="/memory"
      delay={0.08}
      empty="Nothing being pursued yet - habits and goals land here."
      footer={
        <Button
          type="button"
          variant="ghost"
          className="mt-1 h-11 w-full justify-start px-0 text-[13.5px] font-medium text-quiet hover:bg-transparent hover:text-foreground"
        >
          <Plus className="size-4" aria-hidden />
          New pursuit
        </Button>
      }
    >
      {habits.map((p) => (
        <HabitRow
          key={p.id}
          p={p}
          onOpen={() => onOpen({ kind: "pursuit", data: p })}
          onToggle={() => onToggleHabit(p.id)}
        />
      ))}
      {goals.length > 0 ? <GroupLabel key="goals" label="Goals" count={goals.length} /> : null}
      {goals.map((p) => (
        <GoalRow key={p.id} p={p} onOpen={() => onOpen({ kind: "pursuit", data: p })} />
      ))}
    </BoardCard>
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
  // A coached pursuit tracks its own day inside its cockpit, so it gets no
  // tick box here. Leaving one would let a tap write done_today behind the
  // programme's back, and the row would then claim a day was complete that
  // the cockpit had never seen.
  const coached = isCoached(p);
  const meta = [
    coached ? "programme" : p.cadence,
    !coached && p.streakDays ? `${p.streakDays}d streak` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  const shared = {
    title: (
      <span className={cn(!coached && p.doneToday && "text-quiet line-through")}>{p.title}</span>
    ),
    meta,
    onClick: onOpen,
  };

  if (coached) {
    return <ListRow {...shared} leading={<Brain className="size-4" aria-hidden />} />;
  }

  return (
    <ListRow
      {...shared}
      tone={p.doneToday ? "success" : "quiet"}
      leadingAction={
        <motion.button
          type="button"
          whileTap={{ scale: 0.85 }}
          onClick={(e) => {
            e.stopPropagation();
            onToggle();
          }}
          aria-label={p.doneToday ? "Uncheck habit" : "Check habit"}
          /* 18px on the page, 44px to a thumb - see TodosCard for the why. */
          className="group -m-[13px] inline-flex size-11 shrink-0 items-center justify-center transition-colors"
        >
          <span
            className={cn(
              "inline-flex size-[18px] items-center justify-center rounded-full border transition-colors",
              p.doneToday
                ? "border-brand bg-brand text-brand-foreground"
                : "border-border text-quiet group-hover:border-foreground/50",
            )}
          >
            <Check
              className={cn(
                "size-3 transition-opacity",
                p.doneToday ? "opacity-100" : "opacity-0 group-hover:opacity-100",
              )}
              aria-hidden
            />
          </span>
        </motion.button>
      }
    />
  );
}

function GoalRow({ p, onOpen }: { p: Pursuit; onOpen: () => void }) {
  const pct = p.progress
    ? Math.min(100, Math.round((p.progress.current / p.progress.target) * 100))
    : 0;
  const tone = statusTone(p.status);
  const meta = p.progress
    ? `${p.progress.current}/${p.progress.target}${
        p.progress.unit ? ` ${p.progress.unit}` : ""
      } · ${pct}%`
    : undefined;

  return (
    <WorkRow
      kind={p.cadence === "quarterly" ? "quarterly" : p.cadence}
      title={p.title}
      status={p.status ? p.status.replace("_", " ") : undefined}
      tone={tone}
      meta={meta}
      progress={p.progress ? pct / 100 : undefined}
      onClick={onOpen}
    />
  );
}
