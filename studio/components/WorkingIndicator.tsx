"use client";

import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * WorkingIndicator - the persistent "Jarvis is working" affordance shown at
 * the bottom of the conversation whenever a turn is in flight. It exists
 * because the only prior activity signal was the start-of-turn thinking
 * placeholder, which closes on the first delta/tool and never re-appears
 * during the reason-after-tool gaps - so after a tool result the stream
 * looked idle even though the agent was still going (Stop still armed).
 *
 * This sits where the next message will render, so the boss always has an
 * unmistakable "still working" cue between every step: thinking, tool
 * calls, and the gaps in between. The ThinkingBlock owns the live-reasoning
 * case (it shows the streaming trace); the stream suppresses this row while
 * a thinking block is pending so the two don't stack.
 */
export function WorkingIndicator({ label }: { label: string }) {
  return (
    <div className="flex justify-start" data-working-indicator>
      <div
        className={cn(
          "flex min-w-0 max-w-full items-center gap-2 rounded-xl border border-info/40",
          "bg-card px-3 py-2 text-card-foreground",
        )}
      >
        <Loader2 className="size-4 shrink-0 animate-spin text-info" aria-hidden />
        <span
          className="thinking-shimmer whitespace-nowrap text-xs sm:text-sm"
          aria-live="polite"
        >
          {label}
        </span>
      </div>
    </div>
  );
}
