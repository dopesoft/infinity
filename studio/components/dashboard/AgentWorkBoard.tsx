"use client";

import { useMemo, useRef, useState } from "react";
import { motion } from "framer-motion";
import {
  Activity,
  AlertTriangle,
  Brain,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Clock4,
  Cog,
  FileCode,
  HelpCircle,
  Layers,
  Loader2,
  Sparkles,
  Terminal,
  type LucideIcon,
  Workflow,
} from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { clockTime, formatDuration, relTime } from "@/lib/dashboard/format";
import type {
  DashboardItem,
  WorkColumn,
  WorkItem,
  WorkItemKind,
} from "@/lib/dashboard/types";

/* Agent Work Board — Kanban of agent activity.
 *
 * Four columns: queued / running / awaiting you / done (today). Visible
 * proof that Jarvis is *doing things* — cron runs, sentinel watchers,
 * Voyager optimizations, skill verifications, memory ops.
 *
 * Desktop: 4 columns side-by-side, each scrolls internally.
 * Mobile: horizontal-swipe carousel between columns with pager dots,
 *         so a long Done column doesn't push the rest of the dashboard
 *         off-screen.
 */

const KIND_ICON: Record<WorkItemKind, LucideIcon> = {
  cron_run: Clock4,
  voyager_opt: Workflow,
  sentinel: Activity,
  skill_run: Cog,
  workflow: Workflow,
  trust: Terminal,
  code_proposal: FileCode,
  curiosity: HelpCircle,
  memory_op: Brain,
  reflection: Sparkles,
};

const COLUMNS: { key: WorkColumn; label: string; tone: string; Icon: LucideIcon }[] = [
  { key: "queued", label: "Queued", tone: "text-muted-foreground", Icon: Layers },
  { key: "running", label: "Running", tone: "text-info", Icon: Loader2 },
  { key: "awaiting", label: "Awaiting you", tone: "text-rose-400", Icon: AlertTriangle },
  { key: "done", label: "Done today", tone: "text-success", Icon: CheckCircle2 },
];

export function AgentWorkBoard({
  items,
  onOpen,
}: {
  items: WorkItem[];
  onOpen: (item: DashboardItem) => void;
}) {
  const [colIdx, setColIdx] = useState(0);
  const trackRef = useRef<HTMLDivElement>(null);

  const grouped = useMemo(() => {
    const m: Record<WorkColumn, WorkItem[]> = { queued: [], running: [], awaiting: [], done: [] };
    for (const it of items) m[it.column].push(it);
    return m;
  }, [items]);

  function snapToCol(i: number) {
    setColIdx(i);
    const el = trackRef.current;
    if (!el) return;
    const card = el.children[i] as HTMLElement | undefined;
    if (card) el.scrollTo({ left: card.offsetLeft, behavior: "smooth" });
  }

  const totalAwaiting = grouped.awaiting.length;

  return (
    <Section
      title="Agent work"
      Icon={Workflow}
      delay={0.35}
      badge={totalAwaiting > 0 ? `${totalAwaiting} awaiting` : undefined}
      action={{ label: "open cron", href: "/cron" }}
    >
      {/* Desktop: 4-column grid */}
      <div className="hidden grid-cols-4 gap-3 lg:grid">
        {COLUMNS.map((c) => (
          <KanbanColumn
            key={c.key}
            col={c}
            items={grouped[c.key]}
            onOpen={onOpen}
            variant="desktop"
          />
        ))}
      </div>

      {/* Mobile: horizontal-swipe carousel */}
      <div className="space-y-2 lg:hidden">
        <div
          ref={trackRef}
          className="-mx-3 flex snap-x snap-mandatory overflow-x-auto px-3 pb-1 scroll-touch no-scrollbar"
          onScroll={(e) => {
            const el = e.currentTarget;
            const w = el.clientWidth;
            const i = Math.round(el.scrollLeft / Math.max(1, w * 0.86));
            if (i !== colIdx) setColIdx(i);
          }}
        >
          {COLUMNS.map((c) => (
            <div key={c.key} className="w-[86%] shrink-0 snap-start pr-3 last:pr-0">
              <KanbanColumn col={c} items={grouped[c.key]} onOpen={onOpen} variant="mobile" />
            </div>
          ))}
        </div>
        <div className="flex items-center justify-between">
          <button
            type="button"
            onClick={() => snapToCol(Math.max(0, colIdx - 1))}
            aria-label="Previous column"
            className="inline-flex size-8 items-center justify-center rounded-md border bg-card text-muted-foreground hover:text-foreground disabled:opacity-40"
            disabled={colIdx === 0}
          >
            <ChevronLeft className="size-4" aria-hidden />
          </button>
          <div className="flex items-center gap-1.5">
            {COLUMNS.map((c, i) => (
              <button
                key={c.key}
                type="button"
                onClick={() => snapToCol(i)}
                aria-label={`Go to ${c.label}`}
                className={cn(
                  "h-1.5 rounded-full transition-all",
                  i === colIdx ? "w-6 bg-foreground" : "w-1.5 bg-muted-foreground/30",
                )}
              />
            ))}
          </div>
          <button
            type="button"
            onClick={() => snapToCol(Math.min(COLUMNS.length - 1, colIdx + 1))}
            aria-label="Next column"
            className="inline-flex size-8 items-center justify-center rounded-md border bg-card text-muted-foreground hover:text-foreground disabled:opacity-40"
            disabled={colIdx === COLUMNS.length - 1}
          >
            <ChevronRight className="size-4" aria-hidden />
          </button>
        </div>
      </div>
    </Section>
  );
}

function KanbanColumn({
  col,
  items,
  onOpen,
  variant,
}: {
  col: (typeof COLUMNS)[number];
  items: WorkItem[];
  onOpen: (item: DashboardItem) => void;
  variant: "desktop" | "mobile";
}) {
  const isRunning = col.key === "running";
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      <header className="flex items-center justify-between gap-2 border-b bg-muted/20 px-3 py-2">
        <div className="flex items-center gap-2">
          <col.Icon
            className={cn("size-3.5", col.tone, isRunning && items.length > 0 && "animate-spin")}
            aria-hidden
          />
          <span className={cn("text-[11px] font-semibold uppercase tracking-wider", col.tone)}>
            {col.label}
          </span>
        </div>
        <span className="inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-foreground/10 px-1.5 font-mono text-[10px] font-semibold text-foreground">
          {items.length}
        </span>
      </header>
      <ul
        className={cn(
          "divide-y divide-border/60 overflow-y-auto scroll-touch",
          variant === "desktop" ? "max-h-[320px]" : "max-h-[280px]",
        )}
      >
        {items.length === 0 ? (
          <li className="px-3 py-6 text-center text-[11px] text-muted-foreground">
            {col.key === "awaiting"
              ? "Nothing waiting on you."
              : col.key === "running"
                ? "Nothing currently running."
                : col.key === "queued"
                  ? "Queue is empty."
                  : "No completions yet today."}
          </li>
        ) : (
          items.map((it) => (
            <li key={it.id}>
              <WorkRow it={it} onClick={() => onOpen({ kind: "work", data: it })} />
            </li>
          ))
        )}
      </ul>
    </div>
  );
}

function WorkRow({ it, onClick }: { it: WorkItem; onClick: () => void }) {
  const Icon = KIND_ICON[it.kind] ?? Cog;
  const isRunning = it.column === "running";
  const meta =
    it.column === "queued"
      ? it.scheduledFor
        ? `${clockTime(it.scheduledFor)}`
        : "queued"
      : it.column === "running"
        ? it.startedAt
          ? `since ${clockTime(it.startedAt)}`
          : "running"
        : it.column === "awaiting"
          ? "needs you"
          : it.durationMs
            ? `${formatDuration(it.durationMs)} · ${relTime(it.finishedAt)}`
            : relTime(it.finishedAt);
  return (
    <motion.button
      type="button"
      onClick={onClick}
      whileHover={{ x: 2 }}
      transition={{ duration: 0.12 }}
      className="flex w-full items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/40"
    >
      <span
        className={cn(
          "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md border",
          isRunning
            ? "border-info/40 bg-info/10 text-info"
            : "border-border bg-muted text-muted-foreground",
        )}
      >
        <Icon className={cn("size-3", isRunning && "animate-spin")} aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[13px] font-medium text-foreground">{it.title}</p>
        {it.subtitle ? (
          <p className="truncate text-[11px] text-muted-foreground">{it.subtitle}</p>
        ) : null}
        <p
          className="mt-0.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground"
          suppressHydrationWarning
        >
          {meta}
        </p>
      </div>
    </motion.button>
  );
}
