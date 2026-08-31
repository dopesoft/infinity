"use client";

import { useEffect, useState } from "react";
import { ChevronDown, Plus, Undo2, Archive } from "lucide-react";
import { agentStateChip, type AgentState } from "@/components/StatusPill";
import { cn } from "@/lib/utils";
import { Chip, ChipGroup } from "@/components/ui/chip";
import { SessionsDrawer } from "@/components/SessionsDrawer";
import { sessionDisplayName } from "@/lib/sessions";

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
 * Reads "New Conversation" until the namer runs at the end of the first
 * turn - a hex slug of the id is a machine reference, not a name. Once it
 * is named the title updates live via the realtime mem_sessions
 * subscription. The label comes from lib/sessions so the drawer agrees.
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
  pinnedActions,
  agentState = "idle",
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
  /** Controls that stay on the row at every width, right of the status chip.
   *  Navigation, not status: the workbench door belongs here. */
  pinnedActions?: React.ReactNode;
  /** Drives the status chip, which below lg is also the disclosure for
   *  everything else. */
  agentState?: AgentState;
}) {
  const [mounted, setMounted] = useState(false);
  const [trayOpen, setTrayOpen] = useState(false);
  useEffect(() => setMounted(true), []);

  const status = agentStateChip[agentState];

  const displayName = sessionDisplayName({ name: sessionName, id: sessionId });

  return (
    // Compact bar (h-10): the row sits between the global header (h-14)
    // and the workspace columns. Every control in it is a <ChipGroup> at
    // h-7, so the row has one baseline instead of the four it grew.
    <div className="relative flex h-10 shrink-0 items-center gap-2 border-b bg-background/95 px-3 sm:px-4">
      <div className="flex min-w-0 flex-1 items-center gap-2">
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
      {/* The row keeps growing (status, project, bridge, workbench, info,
          compact, new) and on a phone that is more than fits. It used to
          scroll inside itself, which contained the overflow but left the
          session name crushed to "4bbf-…".

          So below lg only three things stay on the row: the status chip,
          which is the one fact worth a glance, New, which he reaches for
          constantly and should never be behind a disclosure, and the
          workbench door, which is navigation. The status chip carries a
          chevron and drops the rest into a tray under the header. The tray is
          the SAME element as the desktop cluster, repositioned - rendering it
          twice would mount a second BridgePill and double its polling.

          Everything right of the name lives in ONE track, tray included, so
          at lg the status chip is simply the first chip of the cluster
          instead of a lone control marooned mid-row by justify-between. */}
      <div className="flex shrink-0 items-center gap-1.5">
        <ChipGroup>
          <Chip
            raised
            tone={status.tone}
            loud={status.loud}
            dot={status.dot}
            aria-live="polite"
            aria-expanded={trayOpen}
            aria-label={`${status.label}. Show session controls.`}
            onClick={() => setTrayOpen((v) => !v)}
            className="lg:pointer-events-none"
          >
            <span className="inline-flex items-center gap-1">
              {status.label}
              {status.sub ? (
                <>
                  <span className="hidden opacity-40 sm:inline" aria-hidden>·</span>
                  <span className="hidden opacity-75 sm:inline">{status.sub}</span>
                </>
              ) : null}
              <ChevronDown
                className={cn(
                  "size-3 shrink-0 opacity-60 transition-transform lg:hidden",
                  trayOpen && "rotate-180",
                )}
                aria-hidden
              />
            </span>
          </Chip>
        </ChipGroup>
        {pinnedActions}
        {/* New, on the row, and LAST - the same place it sits at lg+, so the
            control does not move when the row collapses. It was reachable
            only by first opening the tray, which made the status chip a menu
            button. The tray copy below is the lg+ one; exactly one of the two
            is ever visible. */}
        <ChipGroup className="lg:hidden">
          <Chip
            raised
            iconOnly
            icon={<Plus />}
            onClick={onNew}
            aria-label="Start a new session"
            title="New session"
          />
        </ChipGroup>

        <div
          className={cn(
            "min-w-0 items-center gap-1.5",
            // lg and up: the cluster sits on the row, as it always did.
            "lg:flex lg:static lg:z-auto lg:flex-nowrap lg:justify-start lg:border-0 lg:bg-transparent lg:p-0 lg:backdrop-blur-none",
            // Below lg: a tray under the header, wrapping rather than
            // scrolling so nothing is parked off the edge. It hangs off the
            // chip that opened it, so it lines up right - a menu opens under
            // its trigger, not across the page from it.
            "absolute inset-x-0 top-10 z-30 flex-wrap justify-end border-b bg-background/95 px-3 py-2 backdrop-blur sm:px-4",
            trayOpen ? "flex" : "hidden lg:flex",
          )}
        >
          {extraActions}
          {/* One track for the things you DO to the session, rather than
              three loose ghost buttons at a fourth height. New stays lifted
              because it is the one he reaches for; the rest rest until
              touched. */}
          <ChipGroup>
            {actionChips}
            {onRewind ? (
              <Chip
                responsiveLabel
                icon={<Undo2 />}
                onClick={onRewind}
                disabled
                aria-label="Rewind to an earlier point"
                title="Rewind (coming soon)"
              >
                Rewind
              </Chip>
            ) : null}
            <Chip
              responsiveLabel
              icon={<Archive />}
              onClick={onClear}
              aria-label="Wrap this up, keep what matters and clear the rest"
              title="Compact session"
            >
              Compact
            </Chip>
            <Chip
              raised
              icon={<Plus />}
              onClick={onNew}
              aria-label="Start a new session"
              title="New session"
              className="max-lg:hidden"
            >
              New
            </Chip>
          </ChipGroup>
        </div>
      </div>
    </div>
  );
}
