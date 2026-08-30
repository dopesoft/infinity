"use client";

import { useMemo } from "react";
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
  onClick,
}: {
  it: WorkItem;
  tone: RowTone;
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

  // Plans and mandates carry a step count; a mandate's steps are its criteria.
  const measurable =
    (it.kind === "plan" || it.kind === "mandate") &&
    typeof it.totalCount === "number" &&
    it.totalCount > 0;
  const done = it.doneCount ?? 0;
  const failed = it.subtitle === "failed" || it.subtitle === "abandoned";

  // The job is the headline; the skills it ran are the ingredients, named in
  // the meta line so one row tells the whole story without two titles.
  const meta = [
    measurable ? `${done}/${it.totalCount}` : "",
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
      live={it.column === "running"}
      meta={<span suppressHydrationWarning>{meta}</span>}
      summary={it.summary}
      progress={measurable ? done / (it.totalCount as number) : undefined}
      onClick={onClick}
    />
  );
}
