"use client";

import { useState } from "react";
import {
  AlertTriangle,
  Check,
  ChevronDown,
  ChevronUp,
  Circle,
  FileText,
  Loader2,
  Minus,
  X,
} from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { useRuns } from "@/lib/runs/useRuns";
import { usePlan } from "@/lib/plans/usePlan";
import type { PlanStep } from "@/lib/dashboard/types";
import { cn } from "@/lib/utils";

// BackgroundJobDock is the always-visible status strip pinned between the
// conversation and the composer. It's the CHAT's live window onto the active
// plan ("the Cortex") - the SAME plan the dashboard Agent Work board shows, so
// chat and dashboard stay in sync (one substrate: mem_plans).
//
// It reads the session's active plan via usePlan (server state + realtime, so
// it survives navigation / refresh / second device). It ALSO watches
// background.build runs via useRuns purely for execution telemetry - whether a
// detached build is in flight and which file it's currently touching - since
// that lives on the run, not the plan. The checklist itself is always the plan;
// there is no second todo list anymore.
//
// Shows whenever there's an active plan for this session OR a background build
// running. ONE progress bar: % from the plan's done/total, or indeterminate
// (pulsing) when a build is running without a plan yet.

const BACKGROUND_KIND = "background.build";

export function BackgroundJobDock({ sessionId }: { sessionId?: string }) {
  const plan = usePlan(sessionId);
  const { runs } = useRuns({ kind: BACKGROUND_KIND });
  const [expanded, setExpanded] = useState(false);

  const activeBuilds = runs.filter((r) => r.status === "running");
  const build = activeBuilds[0];
  const others = activeBuilds.slice(1);
  const currentFile = build?.meta?.currentFile?.trim() ?? "";
  const worker = build?.meta?.worker?.trim() ?? "";
  const backend = build?.meta?.backend?.trim() ?? "";

  // Nothing to show unless a plan is in flight or a build is running.
  if (!plan && activeBuilds.length === 0) return null;

  const steps = plan?.steps ?? [];
  const done = plan?.doneCount ?? 0;
  const total = plan?.totalCount ?? 0;
  const hasPlan = total > 0;
  const pct = hasPlan ? Math.round((done / total) * 100) : null;

  const current =
    steps.find((s) => s.status === "in_progress")?.title ??
    steps.find((s) => s.status === "blocked")?.title ??
    steps.find((s) => s.status === "pending")?.title ??
    (steps.length > 0 ? steps[steps.length - 1].title : "");

  const running =
    activeBuilds.length > 0 || steps.some((s) => s.status === "in_progress");
  const status = hasPlan
    ? `${done}/${total} · ${current}`
    : build?.progress_label?.trim() || "working";
  const title = plan?.title?.trim() || build?.label?.trim() || "Background build";
  const workerDetail = [worker, backend].filter(Boolean).join(" · ");

  return (
    <div className="min-w-0 shrink-0 border-t border-info/30 bg-info/[0.06] px-3 py-2 sm:px-4">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-label={expanded ? "Collapse plan" : "Expand plan"}
        className="flex w-full min-w-0 items-center gap-2.5"
      >
        {running ? (
          <Loader2 className="size-4 shrink-0 animate-spin text-info" />
        ) : plan?.status === "paused" ? (
          <AlertTriangle className="size-4 shrink-0 text-warning" />
        ) : (
          <Circle className="size-4 shrink-0 text-info" />
        )}
        <Progress
          value={pct ?? 8}
          className={cn("h-1.5 flex-1 bg-info/15", pct == null && "animate-pulse")}
        />
        <span className="w-9 shrink-0 text-right text-xs font-semibold tabular-nums text-info">
          {pct != null ? `${pct}%` : ""}
        </span>
        {expanded ? (
          <ChevronDown className="size-4 shrink-0 text-info/70" />
        ) : (
          <ChevronUp className="size-4 shrink-0 text-info/70" />
        )}
      </button>

      {!expanded && (
        <div className="mt-1 flex min-w-0 items-center gap-1.5 pl-[26px] text-[11px] text-muted-foreground">
          <span className="min-w-0 flex-1 truncate font-mono text-foreground/80">{status}</span>
          {others.length > 0 && (
            <span className="ml-auto shrink-0 rounded-full bg-info/15 px-1.5 text-info">
              +{others.length}
            </span>
          )}
        </div>
      )}

      {expanded && (
        <div className="mt-2.5 space-y-2.5 border-t border-info/20 pt-2.5">
          <div className="flex min-w-0 items-center gap-1.5 text-[11px] font-medium text-foreground/80">
            <span className="min-w-0 truncate">{title}</span>
          </div>

          {workerDetail && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <span className="min-w-0 truncate">{workerDetail}</span>
            </div>
          )}

          {steps.length > 0 ? (
            <ul className="space-y-1">
              {steps.map((s) => (
                <li key={s.id} className="flex min-w-0 items-start gap-2 text-xs leading-snug">
                  <StepIcon status={s.status} />
                  <span
                    className={cn(
                      "min-w-0 flex-1 break-words [overflow-wrap:anywhere]",
                      (s.status === "done" || s.status === "skipped") &&
                        "text-muted-foreground line-through",
                      s.status === "in_progress" && "font-medium text-foreground",
                      s.status === "pending" && "text-muted-foreground",
                      s.status === "blocked" && "text-warning",
                      s.status === "failed" && "text-danger",
                    )}
                  >
                    {s.title}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="min-w-0 break-words [overflow-wrap:anywhere] text-xs leading-snug text-foreground">
              {title}
            </p>
          )}

          {currentFile && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <FileText className="size-3 shrink-0" />
              <span className="min-w-0 flex-1 truncate font-mono" title={currentFile}>
                {currentFile}
              </span>
            </div>
          )}

          {others.length > 0 && (
            <div className="space-y-1.5 border-t border-info/20 pt-2.5">
              {others.map((run) => (
                <div key={run.id} className="flex min-w-0 items-center gap-2 text-[11px]">
                  <Loader2 className="size-3 shrink-0 animate-spin text-info/70" />
                  <span className="min-w-0 flex-1 truncate text-foreground/80">
                    {run.label?.trim() || "Background build"}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function StepIcon({ status }: { status: PlanStep["status"] }) {
  switch (status) {
    case "done":
      return <Check className="mt-0.5 size-3.5 shrink-0 text-success" />;
    case "in_progress":
      return <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin text-info" />;
    case "blocked":
      return <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />;
    case "failed":
      return <X className="mt-0.5 size-3.5 shrink-0 text-danger" />;
    case "skipped":
      return <Minus className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/50" />;
    default:
      return <Circle className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/40" />;
  }
}
