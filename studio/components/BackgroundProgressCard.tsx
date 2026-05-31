"use client";

import { FileText, Loader2, Terminal } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

// BackgroundProgressCard renders a live background_build run as a full-width
// card sized to match ToolCallCard (same left-anchored rail), instead of the
// cramped inline bar that used to live inside the agent bubble. It surfaces
// the originating task, the current step (verb + file/command being touched),
// a prominent progress bar, and the percentage — so the boss can actually see
// what Jarvis is doing in the background, not just "step 1: delegate".

// detailIsPath sniffs whether the active step's detail is a file path (→ file
// icon, monospace) versus a shell command / query (→ terminal icon). Cheap
// heuristic: a path has a slash or a dotted extension and no spaces.
function detailIsPath(detail: string): boolean {
  if (!detail || /\s/.test(detail)) return false;
  return detail.includes("/") || /\.[a-z0-9]{1,8}$/i.test(detail);
}

export function BackgroundProgressCard({ message }: { message: ChatMessage }) {
  const pct =
    typeof message.progress === "number"
      ? Math.max(2, Math.min(100, Math.round(message.progress * 100)))
      : null;

  const action = message.progressAction?.trim() || "";
  const detail = message.progressDetail?.trim() || "";
  const step =
    typeof message.progressStep === "number" && message.progressStep > 0
      ? message.progressStep
      : null;
  const task = message.progressTask?.trim() || "";
  // Headline falls back to the wire label ("step 3: edit") when no richer
  // action is present (setup / wrap-up phases).
  const headline = message.text?.trim() || "Working…";
  const isPath = detail ? detailIsPath(detail) : false;

  return (
    <div className="min-w-0 max-w-full overflow-hidden rounded-xl border border-info/40 bg-info/[0.06]">
      {/* Header: spinner + label on the left, big percentage on the right. */}
      <div className="flex items-center justify-between gap-3 border-b border-info/20 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <Loader2 className="size-4 shrink-0 animate-spin text-info" />
          <span className="truncate text-xs font-medium uppercase tracking-wide text-info">
            Background build
          </span>
        </div>
        <span className="shrink-0 text-sm font-semibold tabular-nums text-info">
          {pct != null ? `${pct}%` : "running"}
        </span>
      </div>

      <div className="space-y-2.5 px-3 py-2.5">
        {/* Originating task — what the run is for. */}
        {task && (
          <p className="line-clamp-2 text-sm leading-snug text-foreground">{task}</p>
        )}

        {/* Progress bar — prominent, info-tracked. */}
        <Progress
          value={pct ?? 8}
          className={cn("h-2 bg-info/15", pct == null && "animate-pulse")}
        />

        {/* Current step: verb chip + the file/command being touched, with the
            step counter pinned right. */}
        <div className="flex min-w-0 items-center gap-2 text-xs">
          {action && (
            <span className="inline-flex shrink-0 items-center gap-1 rounded-md bg-info/15 px-1.5 py-0.5 font-medium text-info">
              {detail ? (
                isPath ? (
                  <FileText className="size-3" />
                ) : (
                  <Terminal className="size-3" />
                )
              ) : null}
              {action}
            </span>
          )}
          {detail ? (
            <span
              className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground"
              title={detail}
            >
              {detail}
            </span>
          ) : (
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {headline}
            </span>
          )}
          {step != null && (
            <span className="shrink-0 tabular-nums text-muted-foreground">
              step {step}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
