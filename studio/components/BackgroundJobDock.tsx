"use client";

import { useState } from "react";
import { ChevronDown, ChevronUp, FileText, Loader2 } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { useRuns } from "@/lib/runs/useRuns";
import type { RunDTO } from "@/lib/api";
import { cn } from "@/lib/utils";

// BackgroundJobDock is the always-visible status strip pinned between the
// conversation and the composer. It exists so the boss can keep chatting while
// a background_build runs and STILL see, without scrolling up, that a job is in
// flight + its live progress / current file.
//
// It reads mem_runs via useRuns (NOT the transient WS stream), so the dock —
// progress bar, current step, the whole thing — survives navigation, refresh,
// tab switch, and a second device. The Go side keeps mem_runs.progress +
// progress_label live via Handle.Progress on every step (see background.go).
//
// Collapsed (default): a thin two-row strip — spinner + bar + % on row one,
// task · current file on row two. Tap the chevron to expand for the full task
// text and every in-flight run when more than one is building.

const BACKGROUND_KIND = "background.build";

function pctOf(run: RunDTO): number | null {
  return typeof run.progress === "number"
    ? Math.max(2, Math.min(100, Math.round(run.progress * 100)))
    : null;
}

export function BackgroundJobDock() {
  const { runs } = useRuns({ kind: BACKGROUND_KIND });
  const [expanded, setExpanded] = useState(false);

  const active = runs.filter((r) => r.status === "running");
  if (active.length === 0) return null;

  const primary = active[0];
  const pct = pctOf(primary);
  const task = primary.label?.trim() || "Background build";
  const step = primary.progress_label?.trim() || "";
  const moreCount = active.length - 1;

  return (
    <div className="min-w-0 shrink-0 border-t border-info/30 bg-info/[0.06] px-3 py-2 sm:px-4">
      {/* Row 1 — spinner + progress bar + percentage + expand chevron. The
          whole row is the toggle so it's an easy phone tap target. */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-label={expanded ? "Collapse background job" : "Expand background job"}
        className="flex w-full min-w-0 items-center gap-2.5"
      >
        <Loader2 className="size-4 shrink-0 animate-spin text-info" />
        <Progress value={pct ?? 8} className={cn("h-1.5 flex-1 bg-info/15", pct == null && "animate-pulse")} />
        <span className="w-9 shrink-0 text-right text-xs font-semibold tabular-nums text-info">
          {pct != null ? `${pct}%` : ""}
        </span>
        {expanded ? (
          <ChevronDown className="size-4 shrink-0 text-info/70" />
        ) : (
          <ChevronUp className="size-4 shrink-0 text-info/70" />
        )}
      </button>

      {/* Row 2 — task + current file/step on one truncated line. */}
      <div className="mt-1 flex min-w-0 items-center gap-1.5 pl-[26px] text-[11px] text-muted-foreground">
        <span className="shrink-0 truncate font-medium text-foreground/80">{task}</span>
        {step && (
          <>
            <span className="shrink-0 text-muted-foreground/60">·</span>
            <span className="min-w-0 flex-1 truncate font-mono">{step}</span>
          </>
        )}
        {!step && moreCount <= 0 && (
          <span className="min-w-0 flex-1 truncate">running…</span>
        )}
        {moreCount > 0 && (
          <span className="ml-auto shrink-0 rounded-full bg-info/15 px-1.5 text-info">
            +{moreCount}
          </span>
        )}
      </div>

      {/* Expanded — full task text + every in-flight run. */}
      {expanded && (
        <div className="mt-2.5 space-y-2.5 border-t border-info/20 pt-2.5">
          {active.map((run) => {
            const rPct = pctOf(run);
            return (
              <div key={run.id} className="min-w-0 space-y-1.5">
                <div className="flex items-start justify-between gap-2">
                  <p className="min-w-0 break-words [overflow-wrap:anywhere] text-xs leading-snug text-foreground">
                    {run.label?.trim() || "Background build"}
                  </p>
                  <span className="shrink-0 text-xs font-semibold tabular-nums text-info">
                    {rPct != null ? `${rPct}%` : "running"}
                  </span>
                </div>
                <Progress value={rPct ?? 8} className={cn("h-1.5 bg-info/15", rPct == null && "animate-pulse")} />
                {run.progress_label?.trim() && (
                  <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                    <FileText className="size-3 shrink-0" />
                    <span className="min-w-0 flex-1 truncate font-mono" title={run.progress_label}>
                      {run.progress_label.trim()}
                    </span>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
