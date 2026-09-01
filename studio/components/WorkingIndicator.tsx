"use client";

import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";

/**
 * WorkingIndicator - the "still going" cue for the moments the ledger cannot
 * cover, and only those (MAJORDOMO §6: the ledger absorbs this row).
 *
 * A turn's work IS the activity ledger: while any step is in flight, the
 * ledger's own headline shimmers and carries the one spinner. This row is
 * what is left over — the gaps where there are no steps yet, or none any
 * more: between the boss's message and the agent's first move, and while the
 * final reply streams after the last tool has settled. `ConversationStream`
 * suppresses it whenever the trailing message belongs to a ledger, so there
 * is never a second spinner on screen.
 *
 * Majordomo shape: no card, no border. A brand spinner and the headline in
 * the voice face, shimmering, sitting on the page like every other line of
 * the transcript.
 */
export function WorkingIndicator({ label }: { label: string }) {
  return (
    <div className="flex justify-start" data-working-indicator>
      <div className="flex min-h-11 min-w-0 max-w-full items-center gap-2.5">
        <Spinner className="size-[18px] shrink-0 text-brand" aria-hidden />
        <span
          className={cn("min-w-0 truncate font-voice text-[15.5px] leading-[1.55]", "thinking-shimmer")}
          aria-live="polite"
        >
          {label}
        </span>
      </div>
    </div>
  );
}
