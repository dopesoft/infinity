"use client";

import * as React from "react";
import { ChevronRight } from "lucide-react";

import { ActivityStep } from "@/components/chat/ActivityStep";
import { StatusDot } from "@/components/ui/list-row";
import {
  activityIsLive,
  coalesce,
  firstSentence,
  formatDuration,
  headlineFor,
  summaryFor,
} from "@/lib/chat/activity";
import type { ActivityItem } from "@/lib/chat/activity";
import { useNow } from "@/lib/useNow";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * ActivityLedger — a turn's work, as one quiet ledger (MAJORDOMO §6).
 *
 * Replaces `TurnWorkBlock` and absorbs `WorkingIndicator`. A turn that made
 * eleven tool calls reads as ONE headline and a short list of lines, and once
 * it settles it reads as a single sentence: "Worked for 2m 14s · revised the
 * plan, ran 3 commands, edited 2 files."
 *
 * What it owns:
 *  - the headline row: a pulsing brand dot, `headlineFor(...)` shimmering in
 *    the voice face, and the step count + elapsed right-aligned in tabular
 *    figures. While a turn is live this row IS the working indicator — which
 *    is why `ConversationStream` only renders the standalone one when a turn
 *    has no steps yet. Two "still working" signals is one too many.
 *  - the faint left rail down the steps, which is the only chrome here. No
 *    border, no card: sections separate by tone, not by boxes (§1.2).
 *  - the live → settled fold. Open while it runs so the boss can watch him
 *    work; collapses to the summary the moment it settles — unless he opened
 *    or closed it himself, in which case his choice stands.
 *
 * What it does NOT own: any word on any row, and any status. Those come from
 * `lib/chat/activity.ts` via `coalesce`, which is where they are tested.
 */
export function ActivityLedger({
  items,
  /**
   * The turn's current narration, when the caller has it — the plan's active
   * step, say. `headlineFor` already falls back to the turn's own interim note
   * and then to the live row's verb, so passing nothing is a valid choice, not
   * a missing feature.
   */
  narration,
  className,
}: {
  items: ChatMessage[];
  narration?: string;
  className?: string;
}) {
  // Drop the churn that would render an EMPTY row. A thinking block that ended
  // with no trace (the provider had extended thinking off) and an interim
  // bubble that never got any text are both nothing to look at: the old
  // ThinkingBlock hid itself in exactly this case, and "an empty row is of no
  // use to me" is a standing rule. Gated here, at the chokepoint, rather than
  // left to each row to remember. A PENDING empty one stays - that is the live
  // "he is thinking" signal, and it is the whole point of the row.
  const steppable = React.useMemo(
    () =>
      items.filter(
        (m) =>
          m.role === "tool" ||
          m.pending ||
          (m.text ?? "").trim().length > 0,
      ),
    [items],
  );
  const activity = React.useMemo(() => coalesce(steppable), [steppable]);
  const live = activityIsLive(activity);
  // A turn that ended holding something the boss has to decide, or something
  // that broke, does NOT get to fold itself away behind a tidy summary. That
  // is the whole "empty-because-broken must never read as empty-because-fine"
  // law (CLAUDE.md, self-healing) applied to a transcript: a red row inside a
  // collapsed "Worked for 4s" reads exactly like a clean turn.
  const waiting = activity.some((i) => i.status === "approval");
  const broke = activity.some((i) => i.status === "error");
  const held = waiting || broke;
  const [open, setOpen] = React.useState<boolean>(live || held);
  const [touched, setTouched] = React.useState(false);

  React.useEffect(() => {
    if (!touched) setOpen(live || held);
  }, [live, held, touched]);

  // Ticks only while the turn is live; a settled ledger reads its own stamps,
  // so the elapsed freezes exactly where the work stopped.
  const tick = useNow(live);
  const now = tick || Date.now();

  if (activity.length === 0) return null;

  const start = Math.min(...activity.map((i) => i.startedAt));
  const end = live ? now : Math.max(...activity.map((i) => i.endedAt ?? i.startedAt));
  const elapsed = formatDuration(Math.max(0, end - start));

  // §6: one spinner on screen. The ledger nominates the first running row and
  // every other row — including the members of an opened group — gets the dot.
  const spinnerId = activity.find((i) => i.status === "running")?.id;

  const headline = live
    ? headlineFor(activity, narration)
    : summaryFor(activity, tick || undefined);

  // Don't say the same sentence twice, 40px apart. While a turn is live the
  // headline IS his interim narration ("Let me look at what the migration
  // does"), and that narration is also sitting in `activity` as a note row
  // whose meta is the very same text. Rendering both put the identical
  // sentence on screen twice - once shimmering at the top, once as a
  // "Talked it through" row directly beneath it.
  //
  // Drop only the ONE note that supplied the headline, matched by identity
  // rather than by kind: if he narrated twice in a turn, the earlier note is
  // still his words and still earns a row. Settled ledgers are untouched,
  // because their headline is the summary, not his narration.
  // Deliberately NOT a hook: this sits below an early return, and a
  // conditionally-called hook is a rules-of-hooks violation that fails the
  // build. It is a short scan over rows that are already in memory, so
  // memoising it bought nothing anyway.
  const headlineNoteId =
    live && !narration ? headlineNoteIdOf(activity, headline) : undefined;
  const rows = headlineNoteId
    ? activity.filter((i) => i.id !== headlineNoteId)
    : activity;
  // Count what is actually on screen. The narration promoted to the headline
  // is his voice, not a step he took, so counting it would read "8 steps"
  // above 7 rows. Elapsed still spans the whole turn, which is correct: the
  // time is real whether or not a row survived.
  const steps = rows.reduce((n, i) => n + i.count, 0);

  return (
    <div className={cn("flex justify-start", className)}>
      <div className="w-full min-w-0 max-w-full sm:max-w-[80%]">
        <button
          type="button"
          onClick={() => {
            setTouched(true);
            setOpen((v) => !v);
          }}
          aria-expanded={open}
          className="flex min-h-11 w-full min-w-0 max-w-full items-center gap-2.5 py-1.5 text-left"
        >
          {/* One alive signal (§1.4): brand is happening now, amber is waiting
              on the boss, red broke. Everything else is grey. */}
          <StatusDot
            tone={live ? "brand" : waiting ? "warning" : broke ? "danger" : "quiet"}
            pulse={live}
          />
          <span
            className={cn(
              "min-w-0 flex-1 font-voice text-[15.5px] leading-[1.55]",
              // Live: one line beside the counter, because it re-renders every
              // second and must not reflow the row. Settled: the summary is
              // the whole turn in one sentence, so it wraps rather than losing
              // its tail ("… edited 2 files" is the part that matters).
              live ? "truncate thinking-shimmer" : "line-clamp-2 text-muted-foreground",
            )}
            aria-live={live ? "polite" : undefined}
          >
            {headline}
          </span>
          {live ? (
            <span
              className="shrink-0 font-mono text-[12px] tabular-nums text-quiet"
              suppressHydrationWarning
            >
              {steps}
              <span className="hidden sm:inline"> step{steps === 1 ? "" : "s"}</span> · {elapsed}
            </span>
          ) : null}
          <ChevronRight
            className={cn(
              "size-4 shrink-0 text-quiet transition-transform duration-150",
              open && "rotate-90",
            )}
            aria-hidden
          />
        </button>

        {open ? (
          <div className="mt-0.5 flex min-w-0 max-w-full flex-col border-l border-hairline pl-3">
            {rows.map((item) => (
              <ActivityStep key={item.id} item={item} spinner={item.id === spinnerId} />
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

/**
 * headlineNoteIdOf - which coalesced note (if any) supplied the live headline.
 *
 * `headlineFor` walks the turn's notes newest-first and returns the first
 * sentence of the newest assistant one. This finds that same item so the
 * ledger can drop its row, matching by the resulting sentence rather than
 * just taking the last note, so the two can't quietly disagree.
 */
function headlineNoteIdOf(items: ActivityItem[], headline: string): string | undefined {
  if (!headline) return undefined;
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i];
    if (item.kind !== "note") continue;
    if (item.messages[0]?.role !== "assistant") continue;
    if (firstSentence(item.messages[0].text ?? "") === headline) return item.id;
  }
  return undefined;
}
