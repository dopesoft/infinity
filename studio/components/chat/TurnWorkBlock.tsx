"use client";

import { useEffect, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight, Loader2, Wrench } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * TurnWorkBlock - folds a turn's working churn (the paragraph Jarvis narrates
 * before each tool call, the tool cards, the thinking blocks) into ONE
 * collapsible row, so the conversation reads as: your message, what he did
 * (one line, tap to open), his reply.
 *
 * Open while the turn is live so the boss can watch him work (he wants to
 * SEE the coding); auto-collapses the moment the turn settles. Decision
 * cards (plan / skill proposals, agent teams) are never folded: the caller
 * keeps those at the top level.
 */

function formatDuration(ms: number): string {
  if (ms < 1000) return "under a second";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

export function isWorkLive(items: ChatMessage[]): boolean {
  return items.some(
    (m) => m.pending || (m.role === "tool" && !!m.toolCall && !m.toolResult && !m.interrupted),
  );
}

export function TurnWorkBlock({
  items,
  renderItem,
}: {
  items: ChatMessage[];
  renderItem: (m: ChatMessage) => ReactNode;
}) {
  const live = isWorkLive(items);
  const [open, setOpen] = useState<boolean>(live);
  const [touched, setTouched] = useState(false);

  // Auto-collapse when the work settles, unless the boss opened/closed it
  // himself while it ran.
  useEffect(() => {
    if (!touched) setOpen(live);
  }, [live, touched]);

  const toolCount = items.filter((m) => m.role === "tool").length;
  const started = Math.min(...items.map((m) => m.createdAt));
  const ended = Math.max(...items.map((m) => m.endedAt ?? m.createdAt));
  const label = live
    ? `Working… ${toolCount} step${toolCount === 1 ? "" : "s"} so far`
    : `Worked for ${formatDuration(ended - started)} · ${toolCount} step${toolCount === 1 ? "" : "s"}`;

  return (
    <div className="flex justify-start">
      <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
        <button
          type="button"
          onClick={() => {
            setTouched(true);
            setOpen((v) => !v);
          }}
          aria-expanded={open}
          className={cn(
            "flex min-h-11 w-full min-w-0 items-center gap-2 rounded-xl border border-border/60 bg-muted/40 px-3 py-2 text-left text-xs text-muted-foreground",
            live && "border-info/40",
          )}
        >
          {live ? (
            <Loader2 className="size-3.5 shrink-0 animate-spin text-info" aria-hidden />
          ) : (
            <Wrench className="size-3.5 shrink-0" aria-hidden />
          )}
          <span className="min-w-0 flex-1 truncate">{label}</span>
          {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
        </button>
        {open && <div className="mt-2 space-y-3 border-l border-border/60 pl-2 sm:pl-3">{items.map(renderItem)}</div>}
      </div>
    </div>
  );
}
