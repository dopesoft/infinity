"use client";

import { AlertCircle, CheckCircle2, Cpu, MessageSquare, ShieldAlert, Sparkles, Wrench, XCircle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { TraceEventDTO } from "@/lib/api";

/* TraceTimeline — slim left sidebar listing every event in a turn.
 *
 * One `<button>` per event. Selected event gets the info ring. The icon +
 * label combo gives the boss a glanceable vertical map of "what happened
 * this turn": user prompt → tool call → tool result → assistant reply →
 * complete. Predictions render as a child line under their matching tool
 * call so the eye reads them as annotation, not interrupt.
 */
type Props = {
  events: TraceEventDTO[];
  selectedId: string | null;
  onSelect: (e: TraceEventDTO) => void;
};

function iconFor(kind: string) {
  switch (kind) {
    case "user":
      return MessageSquare;
    case "assistant":
      return Sparkles;
    case "tool_call":
      return Wrench;
    case "tool_result":
      return CheckCircle2;
    case "tool_error":
      return XCircle;
    case "gate":
      return ShieldAlert;
    case "prediction":
      return Cpu;
    case "session_start":
    case "session_end":
      return AlertCircle;
    default:
      return AlertCircle;
  }
}

function labelFor(e: TraceEventDTO): string {
  switch (e.kind) {
    case "user":
      return "User prompt";
    case "assistant":
      return "Assistant reply";
    case "tool_call":
      return e.tool_name || "Tool call";
    case "tool_result":
      return `${e.tool_name || "Tool"} result`;
    case "tool_error":
      return `${e.tool_name || "Tool"} error`;
    case "gate":
      return `Gate · ${e.tool_name || ""}`.trim();
    case "prediction":
      return `Prediction · ${e.tool_name || ""}`.trim();
    case "session_start":
      return "Session start";
    case "session_end":
      return "Session end";
    default:
      return e.hook_name || e.kind;
  }
}

function colorFor(kind: string): string {
  switch (kind) {
    case "user":
      return "text-foreground";
    case "assistant":
      return "text-info";
    case "tool_call":
      return "text-foreground";
    case "tool_result":
      return "text-success";
    case "tool_error":
      return "text-danger";
    case "gate":
      return "text-warning";
    case "prediction":
      return "text-muted-foreground";
    default:
      return "text-muted-foreground";
  }
}

export function TraceTimeline({ events, selectedId, onSelect }: Props) {
  if (!events.length) {
    return (
      <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
        No events captured for this turn.
      </div>
    );
  }
  return (
    <div className="space-y-1">
      {events.map((e) => {
        const Icon = iconFor(e.kind);
        const isSelected = selectedId === e.id;
        const isChild = e.kind === "prediction" || e.kind === "gate";
        return (
          <div key={e.id} className={isChild ? "pl-4" : undefined}>
            <button
              type="button"
              onClick={() => onSelect(e)}
              className={cn(
                "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
                "hover:bg-accent/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-info",
                isSelected && "bg-accent/60 ring-1 ring-info/40",
              )}
            >
              <Icon className={cn("mt-0.5 size-3.5 shrink-0", colorFor(e.kind))} />
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium text-foreground">{labelFor(e)}</div>
                {e.surprise !== undefined && e.surprise !== null && (
                  <div className="text-[10px] text-muted-foreground">
                    surprise {e.surprise.toFixed(2)}
                  </div>
                )}
                {e.tool_call_id && e.kind !== "tool_call" && (
                  <div className="truncate text-[10px] text-muted-foreground">
                    {e.tool_call_id.slice(0, 12)}…
                  </div>
                )}
              </div>
            </button>
          </div>
        );
      })}
    </div>
  );
}
