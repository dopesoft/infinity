"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ListChecks } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
} from "@/components/ui/page-tabs";
import { TileCard } from "@/components/dashboard/Section";
import { Chip } from "@/components/dashboard/Chip";
import {
  PlanDetailModal,
  planStatusChipTone,
} from "@/components/dashboard/PlanDetailModal";
import { fetchPlans } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { Plan } from "@/lib/dashboard/types";

/* /plans - the full board for the agent's durable plans ("the Cortex").
 *
 * Active = active + paused (in flight). History = completed/failed/cancelled.
 * Tapping a plan opens the full step timeline in PlanDetailModal. Realtime on
 * mem_plans / mem_plan_steps keeps it live without a manual refresh. Mirrors
 * the /logs page shell so the family reads consistently. */

const LENSES = ["active", "history"] as const;
type Lens = (typeof LENSES)[number];

// History rolls several terminal statuses into one fetch. The server filters
// by a single status, so we fan out and merge for the history lens.
const HISTORY_STATUSES = ["completed", "failed", "cancelled"];

export default function PlansPage() {
  const [lens, setLens] = useTabParam<Lens>("lens", "active", LENSES);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Plan | null>(null);

  const load = useCallback(
    async (opts?: { signal?: AbortSignal; background?: boolean }) => {
      if (!opts?.background) setLoading(true);
      try {
        let rows: Plan[];
        if (lens === "history") {
          const batches = await Promise.all(
            HISTORY_STATUSES.map((s) => fetchPlans(s, opts?.signal)),
          );
          rows = batches
            .flat()
            .sort((a, b) => +new Date(b.updatedAt) - +new Date(a.updatedAt));
        } else {
          rows = await fetchPlans(undefined, opts?.signal); // active + paused
        }
        setPlans(rows);
      } finally {
        setLoading(false);
      }
    },
    [lens],
  );

  useEffect(() => {
    const ctl = new AbortController();
    void load({ signal: ctl.signal });
    return () => ctl.abort();
  }, [load]);

  // Realtime: any plan/step change → debounced background refetch.
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const debounced = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => void load({ background: true }), 500);
  }, [load]);
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
  useRealtime(["mem_plans", "mem_plan_steps"], debounced);

  return (
    <TabFrame>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overflow-x-hidden scroll-touch">
        <main className="mx-auto w-full min-w-0 max-w-5xl flex-1 space-y-5 px-3 pb-6 pt-4 sm:px-4 sm:space-y-6">
          <div className="flex items-center gap-2">
            <ListChecks className="size-5 shrink-0 text-muted-foreground" aria-hidden />
            <h1 className="text-lg font-semibold tracking-tight text-foreground">Plans</h1>
          </div>

          <PageTabs value={lens} onValueChange={(v) => setLens(v as Lens)}>
            <PageTabsList scrollable>
              <PageTabsTrigger value="active">Active</PageTabsTrigger>
              <PageTabsTrigger value="history">History</PageTabsTrigger>
            </PageTabsList>
          </PageTabs>

          {loading && plans.length === 0 ? (
            <p className="py-12 text-center text-sm text-muted-foreground">Loading…</p>
          ) : plans.length === 0 ? (
            <EmptyState lens={lens} />
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4">
              {plans.map((p) => (
                <PlanGridCard key={p.id} plan={p} onOpen={() => setSelected(p)} />
              ))}
            </div>
          )}
        </main>
      </div>

      <PlanDetailModal
        plan={selected}
        open={selected !== null}
        onClose={() => setSelected(null)}
      />
    </TabFrame>
  );
}

function EmptyState({ lens }: { lens: Lens }) {
  return (
    <div className="flex flex-col items-center gap-2 py-16 text-center">
      <ListChecks className="size-8 text-muted-foreground/40" aria-hidden />
      <p className="text-sm font-medium text-foreground">
        {lens === "active" ? "No plans in flight" : "No finished plans yet"}
      </p>
      <p className="max-w-sm text-xs text-muted-foreground">
        {lens === "active"
          ? "When Jarvis takes on a multi-step task, the plan shows up here with live, verifiable step status."
          : "Completed, failed, and cancelled plans land here for the record."}
      </p>
    </div>
  );
}

function PlanGridCard({ plan, onOpen }: { plan: Plan; onOpen: () => void }) {
  const pct =
    plan.totalCount > 0 ? Math.round((plan.doneCount / plan.totalCount) * 100) : 0;
  const current = plan.steps.find(
    (s) => s.status === "in_progress" || s.status === "blocked",
  );
  const nextLabel = current
    ? current.title
    : plan.steps.find((s) => s.status === "pending")?.title;

  return (
    <TileCard onClick={onOpen} className="flex-col items-stretch gap-2.5 p-4">
      <div className="flex min-w-0 items-start gap-2">
        <span className="min-w-0 flex-1 text-sm font-medium leading-snug text-foreground">
          {plan.title}
        </span>
        <Chip tone={planStatusChipTone(plan.status)}>{plan.status}</Chip>
      </div>

      {plan.goal?.trim() ? (
        <p className="line-clamp-2 text-left text-[12px] leading-relaxed text-muted-foreground">
          {plan.goal.trim()}
        </p>
      ) : null}

      <div className="flex items-center gap-2">
        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all",
              plan.status === "failed" ? "bg-danger" : "bg-brand",
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
        <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
          {plan.doneCount}/{plan.totalCount}
        </span>
      </div>

      {nextLabel && plan.status !== "completed" ? (
        <p className="min-w-0 truncate text-left text-[11px] text-muted-foreground">
          <span className="font-mono uppercase tracking-wider opacity-70">next </span>
          {nextLabel}
        </p>
      ) : (
        <p
          className="text-left font-mono text-[10px] uppercase tracking-wider text-muted-foreground/70"
          suppressHydrationWarning
        >
          updated {relTime(plan.updatedAt)}
        </p>
      )}
    </TileCard>
  );
}
