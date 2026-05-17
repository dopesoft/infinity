"use client";

import { useEffect, useState } from "react";
import { ChevronDown, Plus, Undo2, Archive } from "lucide-react";
import { Button } from "@/components/ui/button";
import { SessionsDrawer } from "@/components/SessionsDrawer";

function shortId(id: string): string {
  if (!id) return "-";
  const tail = id.replace(/-/g, "").slice(-8);
  if (tail.length < 8) return tail;
  return `${tail.slice(0, 4)}-${tail.slice(4)}`;
}

function formatStarted(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

/**
 * SessionHeader - the bar across the top of the Live chat surface.
 *
 * The session name is the primary affordance: tap it (or the chevron) to
 * open a bottom-sheet drawer listing every other session with search.
 * Picking a session swaps the conversation in place - no /sessions route
 * to navigate to anymore (we collapsed that surface into this drawer).
 *
 * Falls back to a short hex ID when the session hasn't been auto-named
 * yet (first turn hasn't completed). Once Haiku names it, the title
 * updates live via the realtime mem_sessions subscription.
 */
export function SessionHeader({
  sessionId,
  sessionName,
  startedAt,
  onNew,
  onClear,
  onSwitch,
  onRewind,
  extraActions,
}: {
  sessionId: string;
  sessionName?: string;
  startedAt?: number | null;
  onNew: () => void;
  onClear: () => void;
  onSwitch: (id: string) => void;
  onRewind?: () => void;
  extraActions?: React.ReactNode;
}) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const displayName = sessionName?.trim() || shortId(sessionId);

  return (
    // Compact bar (h-10): the row sits between the global header (h-14)
    // and the workspace columns. Buttons drop to h-7 + text-xs because
    // the contents are dense (name chevron + 2-3 action chips) and the
    // session name already pulls focus.
    <div className="flex h-10 shrink-0 items-center justify-between gap-2 border-b bg-background/95 px-3 sm:px-4">
      <div className="flex min-w-0 items-center gap-2">
        <SessionsDrawer
          currentId={sessionId}
          onSelect={onSwitch}
          onNew={onNew}
          trigger={
            <button
              type="button"
              className="flex h-7 min-w-0 items-center gap-1 rounded-md px-1.5 text-left hover:bg-accent"
              aria-label="Switch session"
            >
              <span className="truncate text-sm font-semibold text-foreground" suppressHydrationWarning>
                {displayName}
              </span>
              <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            </button>
          }
        />
        {startedAt && mounted ? (
          <span
            className="hidden text-[11px] text-muted-foreground sm:inline"
            suppressHydrationWarning
          >
            · started {formatStarted(startedAt)}
          </span>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-0.5">
        {extraActions}
        {onRewind ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={onRewind}
            aria-label="Rewind to a prior turn"
            title="Rewind (coming soon)"
            disabled
            className="h-7 gap-1 px-2 text-xs"
          >
            <Undo2 className="size-3.5" />
            <span className="hidden sm:inline">Rewind</span>
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          onClick={onClear}
          aria-label="Compact session - fold into memory and clear visible context"
          title="Compact session"
          className="h-7 gap-1 px-2 text-xs"
        >
          <Archive className="size-3.5" />
          <span className="hidden sm:inline">Compact</span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={onNew}
          aria-label="Start a new session"
          title="New session"
          className="h-7 gap-1 px-2 text-xs"
        >
          <Plus className="size-3.5" />
          <span className="hidden sm:inline">New</span>
        </Button>
      </div>
    </div>
  );
}
