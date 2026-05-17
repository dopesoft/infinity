"use client";

import { Fragment, useMemo, useState } from "react";
import {
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  Cpu,
  MessageSquare,
  ShieldAlert,
  Sparkles,
  Wrench,
  XCircle,
} from "lucide-react";
import { cn } from "@/lib/utils";
import type { TraceEventDTO } from "@/lib/api";

/* TraceTimeline - slim left sidebar listing every event in a turn.
 *
 * Events group into parent + annotations: any `prediction` / `gate` event
 * that follows a parent (tool_call, user, assistant, …) is treated as a
 * child and rendered indented underneath. Parents with children get a
 * chevron that toggles the group; the row body itself still selects.
 */
type Props = {
  events: TraceEventDTO[];
  selectedId: string | null;
  onSelect: (e: TraceEventDTO) => void;
};

function isChildKind(kind: string): boolean {
  return kind === "prediction" || kind === "gate";
}

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

type Group = { parent: TraceEventDTO; children: TraceEventDTO[] };

export function TraceTimeline({ events, selectedId, onSelect }: Props) {
  const groups = useMemo<Group[]>(() => {
    const out: Group[] = [];
    for (const e of events) {
      if (isChildKind(e.kind) && out.length > 0) {
        out[out.length - 1].children.push(e);
      } else {
        out.push({ parent: e, children: [] });
      }
    }
    return out;
  }, [events]);

  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (!events.length) {
    return (
      <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
        No events captured for this turn.
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {groups.map((g) => {
        const hasChildren = g.children.length > 0;
        const isCollapsed = collapsed.has(g.parent.id);
        return (
          <Fragment key={g.parent.id}>
            <Row
              event={g.parent}
              selectedId={selectedId}
              onSelect={onSelect}
              chevron={
                hasChildren
                  ? { expanded: !isCollapsed, count: g.children.length, onClick: () => toggle(g.parent.id) }
                  : undefined
              }
            />
            {hasChildren && !isCollapsed &&
              g.children.map((c) => (
                <div key={c.id} className="pl-4">
                  <Row event={c} selectedId={selectedId} onSelect={onSelect} />
                </div>
              ))}
          </Fragment>
        );
      })}
    </div>
  );
}

function Row({
  event,
  selectedId,
  onSelect,
  chevron,
}: {
  event: TraceEventDTO;
  selectedId: string | null;
  onSelect: (e: TraceEventDTO) => void;
  chevron?: { expanded: boolean; count: number; onClick: () => void };
}) {
  const Icon = iconFor(event.kind);
  const isSelected = selectedId === event.id;
  return (
    <div className="flex items-stretch gap-1">
      {chevron ? (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            chevron.onClick();
          }}
          aria-label={chevron.expanded ? "Collapse" : "Expand"}
          aria-expanded={chevron.expanded}
          className="flex w-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus:outline-none focus-visible:ring-1 focus-visible:ring-info"
        >
          <ChevronRight
            className={cn("size-3 transition-transform", chevron.expanded && "rotate-90")}
            aria-hidden
          />
        </button>
      ) : (
        <span className="w-4 shrink-0" aria-hidden />
      )}
      <button
        type="button"
        onClick={() => onSelect(event)}
        className={cn(
          "flex w-full min-w-0 items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
          "hover:bg-accent/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-info",
          isSelected && "bg-accent/60 ring-1 ring-info/40",
        )}
      >
        <Icon className={cn("mt-0.5 size-3.5 shrink-0", colorFor(event.kind))} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="truncate font-medium text-foreground">{labelFor(event)}</span>
            {chevron && !chevron.expanded && chevron.count > 0 && (
              <span className="shrink-0 rounded-full bg-muted px-1.5 py-0 font-mono text-[9px] leading-4 text-muted-foreground">
                +{chevron.count}
              </span>
            )}
          </div>
          {event.surprise !== undefined && event.surprise !== null && (
            <div className="text-[10px] text-muted-foreground">
              surprise {event.surprise.toFixed(2)}
            </div>
          )}
          {event.tool_call_id && event.kind !== "tool_call" && (
            <div className="truncate text-[10px] text-muted-foreground">
              {event.tool_call_id.slice(0, 12)}…
            </div>
          )}
        </div>
      </button>
    </div>
  );
}
