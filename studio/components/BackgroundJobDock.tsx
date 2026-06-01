"use client";

import { useState } from "react";
import { Check, ChevronDown, ChevronUp, Circle, FileText, GitBranch, Loader2 } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { useRuns } from "@/lib/runs/useRuns";
import type { RunDTO, RunTodo } from "@/lib/api";
import { cn } from "@/lib/utils";

// BackgroundJobDock is the always-visible status strip pinned between the
// conversation and the composer. It exists so the boss can keep chatting while
// a background_build runs and STILL see, without scrolling up, that a job is in
// flight + its live progress.
//
// It reads mem_runs via useRuns (NOT the transient WS stream), so the dock
// survives navigation, refresh, tab switch, and a second device. The Go side
// keeps the row live: the agent's own todo_write checklist (mem_runs.meta.todos
// → real X/Y progress) plus meta.currentFile per tool call. Works identically
// whether the build runs on the Mac or Cloud bridge.
//
// ONE progress bar, always — collapsed shows it + an "X/Y · action" line;
// the chevron expands to the pinned repo, the checklist, and the current file.
// When the agent didn't author a checklist the bar is indeterminate (pulsing,
// no %) and just shows the current action.

const BACKGROUND_KIND = "background.build";

type Derived = {
  todos: RunTodo[];
  hasTodos: boolean;
  done: number;
  total: number;
  pct: number | null; // null → indeterminate (pulsing bar, no %)
  status: string; // the single-line "X/Y · current" or current action
  repo: string;
  currentFile: string;
};

function derive(run: RunDTO): Derived {
  const todos = run.meta?.todos ?? [];
  const hasTodos = todos.length > 0;
  const done = todos.filter((t) => t.status === "completed").length;
  const total = todos.length;
  const current =
    todos.find((t) => t.status === "in_progress")?.text ??
    todos.find((t) => t.status === "pending")?.text ??
    (total > 0 ? todos[total - 1].text : "");

  const action = run.progress_label?.trim() || "working";
  const pct = hasTodos ? Math.round((done / total) * 100) : null;
  const status = hasTodos ? `${done}/${total} · ${current}` : action;

  return {
    todos,
    hasTodos,
    done,
    total,
    pct,
    status,
    repo: run.meta?.repo?.trim() ?? "",
    currentFile: run.meta?.currentFile?.trim() ?? "",
  };
}

export function BackgroundJobDock() {
  const { runs } = useRuns({ kind: BACKGROUND_KIND });
  const [expanded, setExpanded] = useState(false);

  const active = runs.filter((r) => r.status === "running");
  if (active.length === 0) return null;

  const primary = active[0];
  const d = derive(primary);
  const others = active.slice(1);

  return (
    <div className="min-w-0 shrink-0 border-t border-info/30 bg-info/[0.06] px-3 py-2 sm:px-4">
      {/* The ONE progress bar — spinner + bar + % (only when known) + chevron.
          The whole row is the toggle so it's an easy phone tap target. */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-label={expanded ? "Collapse background job" : "Expand background job"}
        className="flex w-full min-w-0 items-center gap-2.5"
      >
        <Loader2 className="size-4 shrink-0 animate-spin text-info" />
        <Progress
          value={d.pct ?? 8}
          className={cn("h-1.5 flex-1 bg-info/15", d.pct == null && "animate-pulse")}
        />
        <span className="w-9 shrink-0 text-right text-xs font-semibold tabular-nums text-info">
          {d.pct != null ? `${d.pct}%` : ""}
        </span>
        {expanded ? (
          <ChevronDown className="size-4 shrink-0 text-info/70" />
        ) : (
          <ChevronUp className="size-4 shrink-0 text-info/70" />
        )}
      </button>

      {/* Collapsed status line: "X/Y · action" — the ACTION, never the repo
          path. min-w-0 (not shrink-0) so truncate actually works. */}
      {!expanded && (
        <div className="mt-1 flex min-w-0 items-center gap-1.5 pl-[26px] text-[11px] text-muted-foreground">
          <span className="min-w-0 flex-1 truncate font-mono text-foreground/80">{d.status}</span>
          {others.length > 0 && (
            <span className="ml-auto shrink-0 rounded-full bg-info/15 px-1.5 text-info">
              +{others.length}
            </span>
          )}
        </div>
      )}

      {/* Expanded: repo pinned → checklist → current file. The bar above is the
          only progress bar; nothing renders a second one. */}
      {expanded && (
        <div className="mt-2.5 space-y-2.5 border-t border-info/20 pt-2.5">
          {d.repo && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11px] font-medium text-foreground/80">
              <GitBranch className="size-3 shrink-0 text-info" />
              <span className="min-w-0 truncate">{d.repo}</span>
            </div>
          )}

          {d.hasTodos ? (
            <ul className="space-y-1">
              {d.todos.map((t, i) => (
                <li key={i} className="flex min-w-0 items-start gap-2 text-xs leading-snug">
                  {t.status === "completed" ? (
                    <Check className="mt-0.5 size-3.5 shrink-0 text-success" />
                  ) : t.status === "in_progress" ? (
                    <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin text-info" />
                  ) : (
                    <Circle className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/40" />
                  )}
                  <span
                    className={cn(
                      "min-w-0 flex-1 break-words [overflow-wrap:anywhere]",
                      t.status === "completed" && "text-muted-foreground line-through",
                      t.status === "in_progress" && "font-medium text-foreground",
                      t.status === "pending" && "text-muted-foreground",
                    )}
                  >
                    {t.text}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            // No checklist authored — show the full task text so the boss still
            // knows what's running.
            <p className="min-w-0 break-words [overflow-wrap:anywhere] text-xs leading-snug text-foreground">
              {primary.label?.trim() || "Background build"}
            </p>
          )}

          {d.currentFile && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <FileText className="size-3 shrink-0" />
              <span className="min-w-0 flex-1 truncate font-mono" title={d.currentFile}>
                {d.currentFile}
              </span>
            </div>
          )}

          {/* Other in-flight builds — one compact row each. */}
          {others.length > 0 && (
            <div className="space-y-1.5 border-t border-info/20 pt-2.5">
              {others.map((run) => {
                const od = derive(run);
                return (
                  <div key={run.id} className="flex min-w-0 items-center gap-2 text-[11px]">
                    <Loader2 className="size-3 shrink-0 animate-spin text-info/70" />
                    <span className="min-w-0 flex-1 truncate text-foreground/80">
                      {run.label?.trim() || "Background build"}
                    </span>
                    <span className="shrink-0 tabular-nums text-info">
                      {od.pct != null ? `${od.pct}%` : ""}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
