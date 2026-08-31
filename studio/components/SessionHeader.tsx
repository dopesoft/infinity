"use client";

import { useEffect, useState } from "react";
import { ChevronDown, Plus, Undo2, Archive } from "lucide-react";
import { Chip, ChipGroup } from "@/components/ui/chip";
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
  actionChips,
}: {
  sessionId: string;
  sessionName?: string;
  startedAt?: number | null;
  onNew: () => void;
  onClear: () => void;
  onSwitch: (id: string) => void;
  onRewind?: () => void;
  extraActions?: React.ReactNode;
  /** Chips that belong INSIDE the action track, left of Compact. */
  actionChips?: React.ReactNode;
}) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const displayName = sessionName?.trim() || shortId(sessionId);

  return (
    // Compact bar (h-10): the row sits between the global header (h-14)
    // and the workspace columns. Every control in it is a <ChipGroup> at
    // h-7, so the row has one baseline instead of the four it grew.
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
      {/* The action cluster can never push the PAGE sideways. It is the one
          row on /live that keeps growing (status, project, bridge, workbench,
          info, compact, new), and on a 375px phone that is more than fits.
          It scrolls inside itself rather than overflowing the document, so
          nothing is hidden and nothing is lost - the guard is containment,
          not truncation. */}
      <div className="flex min-w-0 shrink items-center gap-1.5 overflow-x-auto scroll-touch no-scrollbar sm:shrink-0">
        {extraActions}
        {/* One track for the things you DO to the session, rather than three
            loose ghost buttons at a fourth height. New stays lifted because
            it is the one he reaches for; the rest rest until touched. */}
        <ChipGroup>
          {actionChips}
          {onRewind ? (
            <Chip
              responsiveLabel
              icon={<Undo2 />}
              onClick={onRewind}
              disabled
              aria-label="Rewind to a prior turn"
              title="Rewind (coming soon)"
            >
              Rewind
            </Chip>
          ) : null}
          <Chip
            responsiveLabel
            icon={<Archive />}
            onClick={onClear}
            aria-label="Compact session - fold into memory and clear visible context"
            title="Compact session"
          >
            Compact
          </Chip>
          <Chip
            raised
            responsiveLabel
            icon={<Plus />}
            onClick={onNew}
            aria-label="Start a new session"
            title="New session"
          >
            New
          </Chip>
        </ChipGroup>
      </div>
    </div>
  );
}
