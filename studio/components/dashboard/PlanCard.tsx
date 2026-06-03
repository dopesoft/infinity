"use client";

import * as React from "react";
import { ListChecks } from "lucide-react";
import { Section, TileCard } from "./Section";
import { ScrollList } from "./ScrollList";
import { Chip } from "./Chip";
import { PlanDetailModal, planStatusChipTone } from "./PlanDetailModal";
import { cn } from "@/lib/utils";
import type { Plan } from "@/lib/dashboard/types";

/* PlanCard - the dashboard surface for the agent's durable plans ("the
 * Cortex"). Shows active + paused plans with their progress; tapping one
 * opens the full step timeline in PlanDetailModal.
 *
 * Built from Section + ScrollList + TileCard (the dashboard primitives) so it
 * matches every other card. Renders nothing when there are no plans in flight,
 * so it stays out of the way until the agent is actually working a plan. */
export function PlanCard({ plans }: { plans: Plan[] }) {
  const [selected, setSelected] = React.useState<Plan | null>(null);

  if (!plans.length) return null;

  return (
    <>
      <Section
        title="Plans"
        Icon={ListChecks}
        delay={0.08}
        badge={plans.length}
        action={{ label: "See all", href: "/plans" }}
      >
        <ScrollList max={3}>
          <ul className="space-y-2">
            {plans.map((p) => (
              <li key={p.id}>
                <PlanRow plan={p} onOpen={() => setSelected(p)} />
              </li>
            ))}
          </ul>
        </ScrollList>
      </Section>

      <PlanDetailModal
        plan={selected}
        open={selected !== null}
        onClose={() => setSelected(null)}
      />
    </>
  );
}

function PlanRow({ plan, onOpen }: { plan: Plan; onOpen: () => void }) {
  const pct =
    plan.totalCount > 0 ? Math.round((plan.doneCount / plan.totalCount) * 100) : 0;
  const current = plan.steps.find(
    (s) => s.status === "in_progress" || s.status === "blocked",
  );
  const nextLabel = current
    ? current.title
    : plan.steps.find((s) => s.status === "pending")?.title;

  return (
    <TileCard onClick={onOpen} className="flex-col items-stretch gap-2 p-3">
      <div className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
          {plan.title}
        </span>
        <Chip tone={planStatusChipTone(plan.status)}>{plan.status}</Chip>
        <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
          {plan.doneCount}/{plan.totalCount}
        </span>
      </div>

      {/* Progress bar */}
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            "h-full rounded-full transition-all",
            plan.status === "failed" ? "bg-danger" : "bg-brand",
          )}
          style={{ width: `${pct}%` }}
        />
      </div>

      {nextLabel ? (
        <p className="min-w-0 truncate text-left text-[11px] text-muted-foreground">
          <span className="font-mono uppercase tracking-wider opacity-70">next </span>
          {nextLabel}
        </p>
      ) : null}
    </TileCard>
  );
}
