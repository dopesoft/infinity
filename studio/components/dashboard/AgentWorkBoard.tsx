"use client";

import { useMemo } from "react";
import { useNow } from "@/lib/useNow";
import { WorkRow, type RowTone } from "@/components/ui/list-row";
import { Board, BoardCard } from "@/components/ui/board";
import { Section } from "./Section";
import { clockTime, formatDuration, relTime } from "@/lib/dashboard/format";
import type {
  DashboardItem,
  WorkColumn,
  WorkItem,
  WorkItemKind,
} from "@/lib/dashboard/types";

/* Agent work - what Jarvis is doing, as a board.
 *
 * IT IS COLUMNS AGAIN, AND THAT WAS THE POINT
 *
 * This surface was four Kanban columns wrapped in four levels of box: a
 * bordered `Section` → four `rounded-xl border` columns → a `border-b
 * bg-muted/20` header bar each → a `size-6 rounded-md border` icon tile per
 * row. On a phone the columns became a horizontal snap-rail with pager dots,
 * so three quarters of the board lived off-screen behind a swipe.
 *
 * The correction removed the boxes and the rail. It ALSO removed the columns,
 * flattening everything into one grouped list, and that was wrong: status is
 * the axis you scan this surface on, and four columns answer "what is running
 * / what needs me / what is queued / what finished" in one glance, where a
 * single list makes you read it. The boxes were the problem, not the columns.
 *
 * So: four columns on `lg`, two at `sm`, one stacked list on a phone - which
 * is the grouped list, and is right there, because a phone has one column of
 * room and a snap-rail hides work. `BoardCard` owns all of that, plus the
 * clip-at-four-rows and the fade that only appears when there is genuinely a
 * fifth row. Each column keeps its count in the head and its own empty line.
 *
 * Titles stay plain English; the engine (cron id, raw skill name) lives in the
 * detail on tap, never in the row.
 */

/** Plain-English name for the kind of work, shown as the row's eyebrow. The
 *  raw kind is an internal token - the boss should never read `voyager_opt`. */
const KIND_LABEL: Record<WorkItemKind, string> = {
  cron_run: "Scheduled job",
  voyager_opt: "Self-improvement",
  sentinel: "Watcher",
  skill_run: "Skill",
  workflow: "Workflow",
  plan: "Plan",
  mandate: "Mandate",
  trust: "Permission",
  code_proposal: "Code change",
  curiosity: "Question",
  memory_op: "Memory",
  reflection: "Reflection",
  phone_errand: "Phone errand",
  phone_call: "Phone call",
};

/* Group order and tone. Running is the one ALIVE thing (brand, pulsing);
 * awaiting is the one thing WAITING on him (warning); queued and done are
 * grey, because a finished job is not an event (§1.4). */
const GROUPS: { key: WorkColumn; label: string; tone: RowTone; empty: string }[] = [
  { key: "running", label: "Running", tone: "brand", empty: "Nothing currently running." },
  { key: "awaiting", label: "Awaiting you", tone: "warning", empty: "Nothing waiting on you." },
  { key: "queued", label: "Queued", tone: "quiet", empty: "Queue is empty." },
  { key: "done", label: "Done today", tone: "quiet", empty: "No completions yet today." },
];

/** How long after its last beat a row may still be animated as live.
 *
 *  Long enough to ride out a slow tool call (a build, a long fetch) without
 *  flickering, short enough that a dead run stops claiming to work within a
 *  couple of minutes rather than the 45 the liveness guard allows. The two are
 *  different jobs: this decides whether we may ANIMATE it, the server guard
 *  decides whether it may still be CALLED running. */
const MOVING_WITHIN_MS = 90_000;

export function AgentWorkBoard({
  items,
  onOpen,
}: {
  items: WorkItem[];
  onOpen: (item: DashboardItem) => void;
}) {
  const grouped = useMemo(() => {
    const m: Record<WorkColumn, WorkItem[]> = { queued: [], running: [], awaiting: [], done: [] };
    for (const it of items) m[it.column].push(it);
    return m;
  }, [items]);

  // A local clock, ticking only while something is in the running column, so
  // a row's animation decays on its own when the beats stop - without a
  // refetch and without waiting for the server to notice.
  const anyRunning = grouped.running.length > 0;
  const now = useNow(anyRunning, 5_000);

  const totalAwaiting = grouped.awaiting.length;
  const anything = GROUPS.some((g) => grouped[g.key].length > 0);

  return (
    <Section
      title="In progress"
      badge={totalAwaiting > 0 ? `${totalAwaiting} awaiting` : undefined}
      action={{ label: "see all", href: "/automations" }}
    >
      {!anything ? (
        /* Every column empty: one quiet line rather than four labelled
         * empties, which is all noise and no information. */
        <p className="py-2 text-[13px] text-quiet">Nothing waiting on you.</p>
      ) : (
        <Board columns={4}>
          {GROUPS.map((g, i) => (
            <BoardCard
              key={g.key}
              title={g.label}
              count={grouped[g.key].length || undefined}
              delay={i * 0.04}
              empty={g.empty}
            >
              {grouped[g.key].map((it) => (
                <Row
                  key={it.id}
                  it={it}
                  tone={g.tone}
                  now={now}
                  onClick={() => onOpen({ kind: "work", data: it })}
                />
              ))}
            </BoardCard>
          ))}
        </Board>
      )}
    </Section>
  );
}

function Row({
  it,
  tone,
  now,
  onClick,
}: {
  it: WorkItem;
  tone: RowTone;
  /** Ticking clock, 0 before the client takes over. */
  now: number;
  onClick: () => void;
}) {
  // Timing reads differently per column: when it is due, how long it has been
  // going, that it needs him, or how long it took and when it landed.
  const timing =
    it.column === "queued"
      ? it.scheduledFor
        ? clockTime(it.scheduledFor)
        : "queued"
      : it.column === "running"
        ? it.startedAt
          ? `since ${clockTime(it.startedAt)}`
          : "running"
        : it.column === "awaiting"
          ? "needs you"
          : it.durationMs
            ? `${formatDuration(it.durationMs)} · ${relTime(it.finishedAt)}`
            : relTime(it.finishedAt);

  // Two shapes of progress, and the bar renders for either.
  //
  //   a step count   plans and mandates, where the fraction is countable work
  //                  and "3/7" is worth printing beside the bar
  //   a fraction     any run reporting its own progress as it goes, which is
  //                  the same live beat the chat dock has always shown
  //
  // Anything that cannot say gets the pulsing dot and no bar. A bar that
  // creeps while nothing is happening is worse than no bar.
  const counted =
    (it.kind === "plan" || it.kind === "mandate") &&
    typeof it.totalCount === "number" &&
    it.totalCount > 0;
  const done = it.doneCount ?? 0;
  // Is it MOVING, on evidence rather than on the say-so of a status column?
  // `now === 0` is the pre-hydration paint: assume nothing is animating, so
  // the server and the first client render agree.
  const movedAt = it.lastMovedAt ? new Date(it.lastMovedAt).getTime() : 0;
  const moving =
    it.column === "running" && now > 0 && movedAt > 0 && now - movedAt < MOVING_WITHIN_MS;

  const fraction = counted
    ? done / (it.totalCount as number)
    : typeof it.progress === "number" && it.progress > 0
      ? Math.min(1, it.progress)
      : undefined;

  // The bar, in three honest states. A row that is moving but cannot quote a
  // number gets the sweep - which says "working" without claiming a
  // percentage - instead of the old answer, which was no bar at all and a
  // pulsing dot carrying the whole claim on no evidence.
  const bar: number | "indeterminate" | undefined =
    fraction !== undefined ? fraction : moving ? "indeterminate" : undefined;
  const failed = it.subtitle === "failed" || it.subtitle === "abandoned";

  // The job is the headline; the skills it ran are the ingredients, named in
  // the meta line so one row tells the whole story without two titles.
  const meta = [
    counted ? `${done}/${it.totalCount}` : "",
    it.subtitle,
    it.skills?.length ? it.skills.join(", ") : "",
    timing,
  ]
    .filter(Boolean)
    .join(" · ");

  const rowTone: RowTone = failed ? "danger" : tone;

  return (
    <WorkRow
      kind={KIND_LABEL[it.kind] ?? "Work"}
      title={it.title}
      tone={rowTone}
      live={moving}
      meta={<span suppressHydrationWarning>{meta}</span>}
      summary={it.summary}
      progress={bar}
      onClick={onClick}
    />
  );
}
