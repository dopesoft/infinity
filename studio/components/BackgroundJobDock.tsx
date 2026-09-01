"use client";

import { useState } from "react";
import {
  AlertTriangle,
  Check,
  ChevronDown,
  ChevronUp,
  Circle,
  FileText,
  Minus,
  X,
} from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Progress } from "@/components/ui/progress";
import { StatusDot } from "@/components/ui/list-row";
import { useCodingRuns } from "@/lib/runs/useCodingRuns";
import { usePlan } from "@/lib/plans/usePlan";
import type { PlanStep } from "@/lib/dashboard/types";
import { cn } from "@/lib/utils";

// BackgroundJobDock is the always-visible status strip pinned between the
// conversation and the composer. It's the CHAT's live window onto the active
// plan ("the Cortex") - the SAME plan the dashboard Agent Work board shows, so
// chat and dashboard stay in sync (one substrate: mem_plans).
//
// It reads the session's active plan via usePlan (server state + realtime, so
// it survives navigation / refresh / second device). It ALSO watches CODING
// runs via useCodingRuns purely for execution telemetry - whether a build is in
// flight and which file it's currently touching - since that lives on the run,
// not the plan. The checklist itself is always the plan; there is no second
// todo list anymore.
//
// It used to watch only `background.build`, which meant the far more common
// case - a `code_agent` run, the boss delegating a coding job inside a turn -
// was invisible in the one place he looks for it, and he was left with a bare
// spinner for eight minutes. Both coding kinds now feed the dock.
//
// Shows whenever there's an active plan for this session OR a coding run is
// live. ONE progress bar: % from the plan's done/total when there's a plan,
// otherwise the run's own progress fraction, and only indeterminate (pulsing)
// when neither has said anything yet.

export function BackgroundJobDock({ sessionId }: { sessionId?: string }) {
  const plan = usePlan(sessionId);
  const { runs: activeBuilds } = useCodingRuns({ runningOnly: true });
  const [expanded, setExpanded] = useState(false);

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
  // The plan's own done/total is the truest measure of "how far in are we",
  // so it wins. Without a plan, fall back to the run's live fraction - which
  // the coding tools now actually write (it was hardcoded 0, so this bar
  // could never move for a code_agent run no matter how long it worked).
  const runPct =
    typeof build?.progress === "number" && build.progress > 0
      ? Math.round(build.progress * 100)
      : null;
  const pct = hasPlan ? Math.round((done / total) * 100) : runPct;

  // What is happening RIGHT NOW, in preference order. A live coding run
  // outranks a blocked step: a step goes 'blocked' when an earlier run was
  // interrupted, and leaving that stale title on screen while a new job is
  // actively editing files is what had the boss looking at a warning sign for
  // work that was in full flight.
  const liveWork = activeBuilds.length > 0 ? (build?.progress_label?.trim() ?? "") : "";
  const current =
    steps.find((s) => s.status === "in_progress")?.title ||
    liveWork ||
    steps.find((s) => s.status === "blocked")?.title ||
    steps.find((s) => s.status === "pending")?.title ||
    (steps.length > 0 ? steps[steps.length - 1].title : "");

  const running =
    activeBuilds.length > 0 || steps.some((s) => s.status === "in_progress");
  const status = hasPlan
    ? `${done}/${total} · ${current}`
    : build?.progress_label?.trim() || "working";
  const title = plan?.title?.trim() || build?.label?.trim() || "Background build";
  const workerDetail = [worker, backend].filter(Boolean).join(" · ");

  // ONE working state across the whole app (MAJORDOMO §1.4). This strip used
  // to be info-blue with its own chevrons and its own bold percentage while
  // the ledger three inches above it was brand-tinted with quiet mono
  // counters — the boss's "three different inventions" for the same idea.
  // Brand means work is happening here, exactly as it does in the ledger, and
  // the numbers are quiet because the sentence is the point, not the maths.
  return (
    <div className="min-w-0 shrink-0 border-t border-hairline bg-brand/[0.05] px-3 py-2.5 sm:px-4">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-label={expanded ? "Collapse plan" : "Expand plan"}
        className="flex w-full min-w-0 items-center gap-2.5"
      >
        {running ? (
          // A spinner, not a dot. MAJORDOMO §6 reserved the one spinner for
          // the ledger's running step, and the dot that replaced it here read
          // as a dead bullet sitting slightly off the bar's centre line - the
          // boss's words. This strip is the persistent "work is happening"
          // surface, so it gets the moving affordance, sized and centred to
          // match the icons in the other two states.
          <Spinner className="size-4 shrink-0 text-brand" aria-hidden />
        ) : plan?.status === "paused" ? (
          <AlertTriangle className="size-4 shrink-0 text-warning" />
        ) : (
          <Circle className="size-4 shrink-0 text-quiet" />
        )}
        <Progress
          value={pct ?? 8}
          className={cn("h-1.5 flex-1 bg-brand/15", pct == null && "animate-pulse")}
        />
        <span className="w-9 shrink-0 text-right font-mono text-[12px] tabular-nums text-quiet">
          {pct != null ? `${pct}%` : ""}
        </span>
        {expanded ? (
          <ChevronDown className="size-4 shrink-0 text-quiet" />
        ) : (
          <ChevronUp className="size-4 shrink-0 text-quiet" />
        )}
      </button>

      {/* One line naming the step he is on (MAJORDOMO §6). It is the plan's
          current step in the voice face - what is actually happening - with
          the count as quiet data beside it. Rendered whether or not the dock
          is expanded, because "17%" alone never told the boss anything. */}
      <div className="mt-1 flex min-w-0 items-center gap-2 pl-[26px]">
        <span className="min-w-0 flex-1 truncate font-voice text-[13.5px] leading-[1.45] text-foreground">
          {current || status}
        </span>
        {hasPlan && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums text-quiet">
            {done}/{total}
          </span>
        )}
        {others.length > 0 && (
          <span className="shrink-0 rounded-full bg-brand/15 px-1.5 font-mono text-[11px] text-brand">
            +{others.length}
          </span>
        )}
      </div>

      {expanded && (
        <div className="mt-2.5 max-h-[38dvh] space-y-2.5 overflow-y-auto border-t border-hairline pt-2.5 scroll-touch">
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
          ) : null}

          {currentFile && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <FileText className="size-3 shrink-0" />
              <span className="min-w-0 flex-1 truncate font-mono" title={currentFile}>
                {currentFile}
              </span>
            </div>
          )}

          {others.length > 0 && (
            <div className="space-y-1.5 border-t border-hairline pt-2.5">
              {others.map((run) => (
                <div key={run.id} className="flex min-w-0 items-center gap-2 text-[11px]">
                  <StatusDot tone="brand" pulse className="mx-[4px]" />
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
      // The step actually being worked gets the moving affordance, aligned to
      // the first line of its label like every other status glyph here.
      return <Spinner className="mt-0.5 size-3.5 shrink-0 text-brand" aria-hidden />;
    case "blocked":
      return <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />;
    case "failed":
      return <X className="mt-0.5 size-3.5 shrink-0 text-danger" />;
    case "skipped":
      return <Minus className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/50" />;
    default:
      return <Circle className="mt-0.5 size-3.5 shrink-0 text-quiet/50" />;
  }
}
