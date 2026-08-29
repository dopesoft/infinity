"use client";

import { useCallback, useEffect, useState } from "react";
import { ArrowLeft, ChevronRight, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { StatusDot } from "@/components/ui/list-row";
import { cn } from "@/lib/utils";
import { fetchCockpit } from "@/lib/pursuits/pc/api";
import { describeProgress } from "@/lib/pursuits/pc/coaching";
import { CoachConversation } from "./CoachConversation";
import { ProgrammePanel } from "./ProgrammePanel";
import type { PCCockpit as PCCockpitData } from "@/lib/pursuits/pc/types";

/* The Psycho-Cybernetics pursuit.
 *
 * Opened by tapping the coached pursuit on the dashboard. It is a coaching
 * SESSION, not a dashboard: the conversation is the surface, and everything
 * that used to be a stacked card (today, identity, memories, history) lives
 * behind one "Programme" affordance.
 *
 * The chrome is deliberately almost nothing. One hairline bar carrying the day
 * counter and that affordance, then the conversation, then the composer. There
 * is no tab strip, no repeated title, no permanent footer of buttons: those are
 * exactly what made the previous version read as a form.
 *
 * Every mutation returns the refreshed cockpit from the server, so this
 * component never patches its own copy of the state and cannot drift from what
 * a coaching chat would see.
 */

export function PCCockpit({
  pursuitId,
  title,
  open,
  onOpenChange,
}: {
  pursuitId: string;
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [cockpit, setCockpit] = useState<PCCockpitData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [programmeOpen, setProgrammeOpen] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        setCockpit(await fetchCockpit(pursuitId, signal));
      } catch (e) {
        if (signal?.aborted) return;
        // A cockpit we could not read is NOT an empty cockpit. Say so, rather
        // than opening a coaching session on a programme we cannot see.
        setError(e instanceof Error ? e.message : "Could not load this programme.");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [pursuitId],
  );

  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    setProgrammeOpen(false);
    void load(ac.signal);
    return () => ac.abort();
  }, [open, load]);

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description="Psycho-Cybernetics coaching session"
      size="full"
      desktopHeight="full"
      bodyClassName="flex min-h-0 min-w-0 flex-col overflow-y-hidden p-0 sm:p-0"
      header={
        <SessionBar
          title={title}
          progress={cockpit ? describeProgress(cockpit) : undefined}
          programmeOpen={programmeOpen}
          onToggleProgramme={() => setProgrammeOpen((v) => !v)}
          showProgramme={Boolean(cockpit)}
        />
      }
    >
      {error ? (
        <div className="mx-auto flex w-full max-w-[38rem] flex-col items-start gap-3 px-4 py-10 sm:px-6">
          <p className="font-voice text-[15.5px] leading-[1.6] text-danger">
            I could not open your programme just now. {error}
          </p>
          <Button variant="outline" onClick={() => void load()}>
            Try again
          </Button>
        </div>
      ) : loading && !cockpit ? (
        <div className="mx-auto flex w-full max-w-[38rem] items-center gap-2 px-4 py-10 text-[13.5px] text-quiet sm:px-6">
          <Loader2 className="size-4 animate-spin" aria-hidden />
          Opening your programme
        </div>
      ) : !cockpit ? null : (
        <div className="flex min-h-0 min-w-0 flex-1">
          <div
            className={cn(
              "flex min-h-0 min-w-0 flex-1 flex-col",
              // On a phone the programme takes the whole surface, so the
              // conversation steps aside rather than sitting behind a sheet.
              programmeOpen && "hidden lg:flex",
            )}
          >
            <CoachConversation
              key={cockpit.pursuit.id}
              cockpit={cockpit}
              onUpdated={setCockpit}
              onLeave={() => onOpenChange(false)}
            />
          </div>

          {programmeOpen ? (
            <aside
              id="pc-programme"
              aria-label="Programme"
              className="min-h-0 w-full min-w-0 overflow-y-auto overflow-x-hidden scroll-touch lg:w-[23rem] lg:shrink-0 lg:border-l lg:border-hairline"
            >
              <div className="px-4 pt-3 sm:px-5 lg:hidden">
                <Button variant="ghost" size="sm" onClick={() => setProgrammeOpen(false)}>
                  <ArrowLeft className="size-4" aria-hidden />
                  Back to the session
                </Button>
              </div>
              <ProgrammePanel title={title} cockpit={cockpit} onUpdated={setCockpit} />
            </aside>
          ) : null}
        </div>
      )}
    </ResponsiveModal>
  );
}

/* The whole of the chrome: a live dot, where the boss is in the cycle, and the
 * way through to the programme. The pursuit title stays as the modal's
 * accessible name (ResponsiveModal renders it sr-only when a custom header is
 * supplied) and is repeated at the top of the programme, so nothing is lost by
 * keeping it out of the boss's eyeline during a session. */
function SessionBar({
  title,
  progress,
  programmeOpen,
  onToggleProgramme,
  showProgramme,
}: {
  title: string;
  progress?: string;
  programmeOpen: boolean;
  onToggleProgramme: () => void;
  showProgramme: boolean;
}) {
  return (
    <header className="flex shrink-0 items-center gap-3 border-b border-hairline px-4 py-2.5 pr-12 sm:px-5 sm:pr-14">
      <StatusDot tone="brand" pulse />
      <span className="min-w-0 flex-1 truncate font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
        {progress ?? title}
      </span>
      {showProgramme ? (
        <Button
          variant="ghost"
          size="sm"
          onClick={onToggleProgramme}
          aria-expanded={programmeOpen}
          aria-controls="pc-programme"
          className="shrink-0 text-quiet"
        >
          Programme
          <ChevronRight
            className={cn("size-4 transition-transform duration-150", programmeOpen && "rotate-90")}
            aria-hidden
          />
        </Button>
      ) : null}
    </header>
  );
}
