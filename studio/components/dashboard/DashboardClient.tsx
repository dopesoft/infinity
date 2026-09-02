"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { SectionBand } from "./Section";
import { Board, BoardBand } from "@/components/ui/board";
import { DashboardHeader } from "./DashboardHeader";
import { PursuitsCard } from "./PursuitsCard";
import { TodosCard } from "./TodosCard";
import { UpcomingCard } from "./UpcomingCard";
import { ReflectionCard } from "./ReflectionCard";
import { SurfacedCard } from "./SurfacedCard";
import { PhoneCard } from "./PhoneCard";
import { FollowUpsCard } from "./FollowUpsCard";
import { AgentWorkBoard } from "./AgentWorkBoard";
import { SavedCard } from "./SavedCard";
import { ActivityCard } from "./ActivityCard";
import { MemoryFooter } from "./MemoryFooter";
import { ObjectViewer } from "./ObjectViewer";
import { AddTodoModal } from "./AddTodoModal";
import { PCCockpit } from "@/components/pursuits/pc/PCCockpit";
import { JobHuntCockpit } from "@/components/pursuits/jh/JobHuntCockpit";
import { updateTodo } from "@/lib/api";
import { useDashboardPrefs } from "@/lib/dashboard/preferences";
import { fetchDashboard, readDashboardCache } from "@/lib/dashboard/fetcher";
import { usePageLoading } from "@/lib/loading";
import type { DashboardResponse } from "@/lib/dashboard/fetcher";
import { useRealtime } from "@/lib/realtime/provider";
import type {
  ActivityEvent,
  Approval,
  Artifact,
  CalendarEvent,
  DailyQuote,
  DashboardItem,
  FollowUp,
  MemoryStats,
  Pursuit,
  Reflection,
  Saved,
  SurfaceItem,
  Todo,
  WorkItem,
} from "@/lib/dashboard/types";
import type { SearchHit } from "@/lib/api";
import { useGlobalSearch } from "@/lib/search/useGlobalSearch";
import { useRecordSheet } from "@/components/search/RecordSheet";
import { SearchResults } from "@/components/dashboard/SearchResults";

/* DashboardClient - the orchestrating client component for the Dashboard
 * tab. Holds local mock state (toggle habits/todos optimistically),
 * routes taps into the ObjectViewer, and lays out every section.
 *
 * Layout rules (Majordomo §2, §8):
 *   • Mobile (<lg): single column scroll, sections stacked top-to-bottom.
 *   • Desktop (lg+): two 3-up rows (what is being raised TO him, then his own
 *     commitments), then Agent work, Reflection, Saved, Activity full-width.
 *   • Sections separate by GROUND, not by chrome: the page alternates plain
 *     and `band` (a full-bleed muted strip) down its length, so neighbouring
 *     areas read as distinct without a single card, header bar, or divider
 *     between them. `card` tone is spent once, on Reflection, because that is
 *     the one section that is a single object he acts on.
 *   • Never nest tones: the sections inside a `SectionBand` are all plain.
 *
 * Alignment: cards in a row line up because they clip at the same ROW COUNT
 * and the grid stretches the cells - not because one card measures itself and
 * hands a pixel height to its neighbours. See `./listHeight` for the bug that
 * rule came from.
 *
 * Search filters across every section's content. When active, sections
 * with zero matches are still rendered so the page structure stays
 * stable - they just say "no matches" inline.
 */

const ZERO_MEMORY_STATS: MemoryStats = {
  newToday: 0,
  promotedToday: 0,
  procedural: 0,
  streakDays: 0,
};

// surfacedWeight ranks the merged "Surfaced by Jarvis" list so genuine
// decisions (tool-permission + code-change approvals) sit at the top and a
// time-sensitive yes/no never hides under low-importance FYIs.
//
// For surface items the question "does this ask me for something?" beats raw
// importance: a card carrying actions needs a decision, one without is an FYI.
// Ranking on importance alone buried a routine proposal (importance 23, two
// buttons) under self-heal receipts (importance 28, nothing to do). So an
// actionable item always outranks any FYI, and importance orders within each
// band rather than across them.
const SURFACE_ACTIONABLE_FLOOR = 50;

function surfacedWeight(it: DashboardItem): number {
  if (it.kind === "approval") {
    if (it.data.kind.startsWith("trust_")) return 100;
    if (it.data.kind === "code_proposal") return 85;
    return 55; // curiosity
  }
  if (it.kind === "surface") {
    const importance = it.data.importance ?? 40;
    const actionable = (it.data.actions?.length ?? 0) > 0;
    return (actionable ? SURFACE_ACTIONABLE_FLOOR : 0) + importance / 2;
  }
  return 0;
}

// Sort on when the card last said something new. A rolling surface card (one
// per cron) keeps its original createdAt, so sorting on that pins tonight's
// failure to the bottom under the date it first appeared.
function surfacedCreatedAt(it: DashboardItem): number {
  if (it.kind === "surface") {
    return new Date(it.data.updatedAt ?? it.data.createdAt).getTime();
  }
  if (it.kind === "approval") {
    return new Date(it.data.createdAt).getTime();
  }
  return 0;
}

export function DashboardClient() {
  // Every section starts empty and is filled only by /api/dashboard.
  // No mock fixtures, no fallback fixtures - if the fetch fails the
  // dashboard shows empty state, not a lie.
  const [pursuits, setPursuits] = useState<Pursuit[]>([]);
  const [todos, setTodos] = useState<Todo[]>([]);
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [followUps, setFollowUps] = useState<FollowUp[]>([]);
  const [work, setWork] = useState<WorkItem[]>([]);
  const [saved, setSaved] = useState<Saved[]>([]);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [activity, setActivity] = useState<ActivityEvent[]>([]);
  const [reflection, setReflection] = useState<Reflection | null>(null);
  const [memoryStats, setMemoryStats] = useState<MemoryStats>(ZERO_MEMORY_STATS);
  // Generic surface contract: items the agent surfaced via `surface_item`,
  // grouped by `surface` key. A new surface the agent invents renders here
  // automatically - no new state field, no new card component.
  const [surfaceItems, setSurfaceItems] = useState<Record<string, SurfaceItem[]>>({});
  // `loading` covers both the first paint and every realtime-driven
  // refetch. The header spinner reads from this so the boss can see the
  // page is in flight instead of staring at empty cards. Initial value
  // is true so the spinner shows immediately while the first fetch
  // hasn't resolved yet.
  const [loading, setLoading] = useState(true);
  // Lights the app mark until the first payload lands. See lib/loading.
  usePageLoading(loading);
  // Header context: who he is and today's line. Both ride the dashboard
  // payload, so they are on screen from the localStorage cache before the
  // network answers rather than popping in a beat later.
  const [bossName, setBossName] = useState("");
  const [quote, setQuote] = useState<DailyQuote | null>(null);

  // Distribute a payload into the section slices. Shared by the cache-first
  // hydrate and every fetch/realtime refresh so they can't drift.
  const applyData = useCallback((data: DashboardResponse) => {
    setPursuits(data.pursuits ?? []);
    setTodos(data.todos ?? []);
    setEvents(data.calendarEvents ?? []);
    setFollowUps(data.followUps ?? []);
    setSaved(data.saved ?? []);
    setArtifacts(data.artifacts ?? []);
    setApprovals(data.approvals ?? []);
    setActivity(data.activity ?? []);
    setWork(data.work ?? []);
    setReflection(data.reflection ?? null);
    setSurfaceItems(data.surfaceItems ?? {});
    setBossName(data.bossName ?? "");
    setQuote(data.quote ?? null);
    if (data.memoryStats) setMemoryStats(data.memoryStats);
  }, []);

  // background=true means a silent refresh (cache already on screen, or a
  // realtime-driven refetch) - don't flash the full-page loading state.
  const load = useCallback(
    async (opts?: { signal?: AbortSignal; background?: boolean }) => {
      if (!opts?.background) setLoading(true);
      try {
        const data = await fetchDashboard(opts?.signal);
        if (!data) return;
        applyData(data);
      } finally {
        setLoading(false);
      }
    },
    [applyData],
  );

  useEffect(() => {
    const ctl = new AbortController();
    // Cache-first: paint last-known data instantly, then revalidate in the
    // background. No more staring at empty cards for 5-7s on every visit.
    const cached = readDashboardCache();
    if (cached) {
      applyData(cached);
      setLoading(false);
    }
    void load({ signal: ctl.signal, background: !!cached });
    return () => ctl.abort();
  }, [load, applyData]);

  // Realtime: re-fetch on ANY table change ("*"), so the dashboard is never
  // stale no matter which subsystem changed — crons, runs, sentinels, skill
  // proposals, calendar, follow-ups, surfaced items, anything the agent
  // touches. The previous hand-maintained 9-table list is exactly what kept
  // drifting: the Agent Work board reads mem_crons/mem_runs/mem_skill_proposals
  // /mem_sentinels/mem_code_proposals/mem_skill_runs, NONE of which were in the
  // list, so those columns only updated on a hard refresh. "*" + the provider's
  // schema-wide subscription means new tables are covered forever with zero
  // edits. Debounced so a burst (a cron firing, dozens of observations) folds
  // into ONE background refetch instead of hammering /api/dashboard.
  const refetchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const debouncedRefetch = useCallback(() => {
    if (refetchTimer.current) clearTimeout(refetchTimer.current);
    refetchTimer.current = setTimeout(() => void load({ background: true }), 500);
  }, [load]);
  useEffect(
    () => () => {
      if (refetchTimer.current) clearTimeout(refetchTimer.current);
    },
    [],
  );
  useRealtime("*", debouncedRefetch);

  // The search box runs the SAME global search as the ⌘K palette (one hook,
  // two consumers). It used to filter only the rows already on screen, which
  // made the one search field on the page he looks at the one that could not
  // find anything he could not already see.
  const [search, setSearch] = useState("");
  const searching = search.trim().length > 0;
  const { groups: searchGroups, hits: searchHits, loading: searchLoading, failed: searchFailed } =
    useGlobalSearch(search);
  const recordSheet = useRecordSheet();
  const [addingTodo, setAddingTodo] = useState(false);
  const [viewing, setViewing] = useState<DashboardItem | null>(null);
  // A pursuit running a bespoke experience opens its own cockpit rather than
  // the generic ObjectViewer. Held as {id,title} instead of the whole row so
  // the cockpit always reads its state from the server.
  const [pcPursuit, setPcPursuit] = useState<{ id: string; title: string } | null>(null);
  const [jhPursuit, setJhPursuit] = useState<{ id: string; title: string } | null>(null);
  const { prefs } = useDashboardPrefs();
  const s = prefs.sections;

  // The one routing decision for every dashboard tap. An ordinary item goes to
  // the ObjectViewer; a pursuit whose `experience` names an app goes to that
  // app. Keeping the branch here means each card still calls one `onOpen` and
  // no card needs to know which experiences exist.
  const openViewer = useCallback((item: DashboardItem) => {
    if (item.kind === "pursuit" && item.data.experience === "psycho_cybernetics") {
      setPcPursuit({ id: item.data.id, title: item.data.title });
      return;
    }
    if (item.kind === "pursuit" && item.data.experience === "job_hunt") {
      setJhPursuit({ id: item.data.id, title: item.data.title });
      return;
    }
    setViewing(item);
  }, []);
  const closeViewer = useCallback(() => setViewing(null), []);

  // Every dashboard row the page is currently holding, by id. Built once so
  // a search hit that IS on the dashboard opens its real, hydrated item -
  // with its Dismiss button, its email body, its progress bar - instead of
  // being downgraded to a generic record fetched back off the server. One id
  // lookup, no per-kind branching.
  const itemsById = useMemo(() => {
    const m = new Map<string, DashboardItem>();
    const put = (item: DashboardItem) => {
      const id = (item.data as { id?: string }).id;
      if (id) m.set(id, item);
    };
    approvals.forEach((a) => put({ kind: "approval", data: a }));
    followUps.forEach((f) => put({ kind: "followup", data: f }));
    Object.values(surfaceItems)
      .flat()
      .forEach((it) => put({ kind: "surface", data: it }));
    work.forEach((w) => put({ kind: "work", data: w }));
    events.forEach((e) => put({ kind: "event", data: e }));
    todos.forEach((t) => put({ kind: "todo", data: t }));
    pursuits.forEach((p) => put({ kind: "pursuit", data: p }));
    saved.forEach((v) => put({ kind: "saved", data: v }));
    artifacts.forEach((a) => put({ kind: "artifact", data: a }));
    activity.forEach((a) => put({ kind: "activity", data: a }));
    return m;
  }, [approvals, followUps, surfaceItems, work, events, todos, pursuits, saved, artifacts, activity]);

  // The one routing decision for a search result. Hydrated if we have it,
  // otherwise the generic record sheet, which fetches the detail from
  // /api/object. Either way it opens IN PLACE - modal on a desktop, drawer on
  // a phone - rather than pushing a route.
  const openHit = useCallback(
    (hit: SearchHit) => {
      const held = itemsById.get(hit.id);
      if (held) {
        openViewer(held);
        return;
      }
      recordSheet.open(hit);
    },
    [itemsById, openViewer, recordSheet],
  );
  const liveViewing = useMemo<DashboardItem | null>(() => {
    if (!viewing) return null;
    const id = (viewing.data as { id?: string }).id;
    if (!id) return viewing;
    switch (viewing.kind) {
      case "approval":
        return approvals.find((a) => a.id === id)
          ? { kind: "approval", data: approvals.find((a) => a.id === id)! }
          : viewing;
      case "followup":
        return followUps.find((f) => f.id === id)
          ? { kind: "followup", data: followUps.find((f) => f.id === id)! }
          : viewing;
      case "surface": {
        const found = Object.values(surfaceItems).flat().find((s) => s.id === id);
        return found ? { kind: "surface", data: found } : viewing;
      }
      case "work":
        return work.find((w) => w.id === id)
          ? { kind: "work", data: work.find((w) => w.id === id)! }
          : viewing;
      case "event":
        return events.find((e) => e.id === id)
          ? { kind: "event", data: events.find((e) => e.id === id)! }
          : viewing;
      case "todo":
        return todos.find((t) => t.id === id)
          ? { kind: "todo", data: todos.find((t) => t.id === id)! }
          : viewing;
      case "pursuit":
        return pursuits.find((p) => p.id === id)
          ? { kind: "pursuit", data: pursuits.find((p) => p.id === id)! }
          : viewing;
      case "saved":
        return saved.find((s) => s.id === id)
          ? { kind: "saved", data: saved.find((s) => s.id === id)! }
          : viewing;
      case "artifact":
        return artifacts.find((a) => a.id === id)
          ? { kind: "artifact", data: artifacts.find((a) => a.id === id)! }
          : viewing;
      case "activity":
        return activity.find((a) => a.id === id)
          ? { kind: "activity", data: activity.find((a) => a.id === id)! }
          : viewing;
      default:
        return viewing;
    }
  }, [viewing, approvals, followUps, surfaceItems, work, events, todos, pursuits, saved, artifacts, activity]);
  const resolveViewerItem = useCallback((item: DashboardItem) => {
    if (item.kind === "approval") {
      const id = item.data.id;
      setApprovals((prev) => prev.filter((a) => a.id !== id));
      setWork((prev) =>
        prev.filter((w) => w.id !== `trust-${id}` && w.id !== `code-${id}`),
      );
      setViewing(null);
      return;
    }
    if (item.kind === "followup") {
      const id = item.data.id;
      setFollowUps((prev) => prev.filter((f) => f.id !== id));
      setViewing(null);
      return;
    }
    if (item.kind === "surface") {
      const id = item.data.id;
      setSurfaceItems((prev) => {
        const next: Record<string, SurfaceItem[]> = {};
        for (const [key, items] of Object.entries(prev)) {
          const kept = items.filter((it) => it.id !== id);
          if (kept.length) next[key] = kept;
        }
        return next;
      });
      setViewing(null);
      return;
    }
    if (item.kind === "activity") {
      const id = item.data.id;
      setActivity((prev) => prev.filter((e) => e.id !== id));
      setViewing(null);
      return;
    }
    if (item.kind === "work") {
      // Agent Work card dismissed/cancelled/denied — drop it from the board
      // optimistically (realtime reconciles the underlying run/plan/proposal).
      const id = item.data.id;
      setWork((prev) => prev.filter((w) => w.id !== id));
      setViewing(null);
      return;
    }
    // Catch-all: any other kind still closes the viewer so a dismiss never
    // leaves the modal hanging open over a resolved item.
    setViewing(null);
  }, []);

  const toggleHabit = useCallback((id: string) => {
    setPursuits((prev) =>
      prev.map((p) =>
        p.id === id
          ? {
              ...p,
              doneToday: !p.doneToday,
              doneAt: !p.doneToday ? new Date().toISOString() : undefined,
              streakDays:
                p.cadence === "daily"
                  ? (p.streakDays ?? 0) + (!p.doneToday ? 1 : -1)
                  : p.streakDays,
            }
          : p,
      ),
    );
  }, []);

  // Toggle optimistically for instant feedback, then persist to mem_tasks so
  // the change survives refresh AND is visible to Jarvis (his task_list reads
  // the same row). On failure we roll the optimistic flip back. The realtime
  // mem_tasks subscription reconciles to server truth either way.
  const toggleTodo = useCallback((id: string) => {
    let nextDone = false;
    setTodos((prev) =>
      prev.map((t) => {
        if (t.id !== id) return t;
        nextDone = !t.done;
        return { ...t, done: nextDone };
      }),
    );
    void updateTodo({ id, status: nextDone ? "done" : "open" }).then((ok) => {
      if (!ok) {
        setTodos((prev) =>
          prev.map((t) => (t.id === id ? { ...t, done: !nextDone } : t)),
        );
      }
    });
  }, []);


  // The unified "Surfaced by Jarvis" list - approvals + every agent surface
  // (alerts, insights, digest, …) merged into one importance-sorted stream.
  // Decisions rank to the top (see surfacedWeight). A new surface key the
  // agent invents flows in here automatically; system/follow-up surfaces are
  // routed elsewhere by the backend so they never appear.
  const surfaced = useMemo<DashboardItem[]>(() => {
    const merged: DashboardItem[] = [
      ...approvals.map((a) => ({ kind: "approval", data: a }) as DashboardItem),
      ...Object.entries(surfaceItems)
        // Calls get their own PhoneCard; excluding the key here is what
        // keeps a call from rendering twice.
        .filter(([key]) => key !== "calls")
        .flatMap(([, items]) => items)
        .map((it) => ({ kind: "surface", data: it }) as DashboardItem),
    ];
    merged.sort((a, b) => {
      const w = surfacedWeight(b) - surfacedWeight(a);
      return w !== 0 ? w : surfacedCreatedAt(b) - surfacedCreatedAt(a);
    });
    return merged;
  }, [approvals, surfaceItems]);

  // Jarvis's phone line: the surface='calls' group, newest first.
  const calls = useMemo<DashboardItem[]>(() => {
    const items = (surfaceItems["calls"] ?? []).map(
      (it) => ({ kind: "surface", data: it }) as DashboardItem,
    );
    items.sort((a, b) => surfacedCreatedAt(b) - surfacedCreatedAt(a));
    return items;
  }, [surfaceItems]);


  return (
    <AppShell>
      {/* min-w-0 + overflow-x-hidden keep the dashboard page-locked on
          mobile: any card with wider-than-viewport content (heartbeat
          artifact JSON, long titles, etc.) gets clipped at the page
          edge instead of pushing the whole page horizontally and
          breaking the fixed-composer / fixed-header invariant the rest
          of the app relies on. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overflow-x-hidden scroll-touch">
        <DashboardHeader
          bossName={bossName}
          quote={quote}
          search={search}
          onSearchChange={setSearch}
        />

        {/* max-w-board, the token — not max-w-6xl. The width law lives in
            tailwind.config so it is one number, not a magic value per page. */}
        <main className="mx-auto w-full min-w-0 max-w-board flex-1 space-y-6 px-4 pb-2 sm:px-6">
          {/* A query takes the page over. The sections are what the dashboard
              is FOR when he is not looking for something specific; while he
              is, they are noise between him and the thing he typed. */}
          {searching ? (
            <SearchResults
              query={search}
              groups={searchGroups}
              count={searchHits.length}
              loading={searchLoading}
              failed={searchFailed}
              onOpen={openHit}
            />
          ) : (
          <>
          {/* PLAIN - what is being raised TO the boss: Surfaced by Jarvis, the
              Phone line, and Email. `grid-cols-1` default is REQUIRED so an
              implicit max-content track can't blow the column past the
              viewport on mobile; lg splits to three. */}
          {(s.approvals || s.followups) && (
            <Board>
              {s.approvals && <SurfacedCard items={surfaced} onOpen={openViewer} />}
              <PhoneCard items={calls} onOpen={openViewer} delay={0.04} />
              {s.followups && (
                <FollowUpsCard followUps={followUps} onOpen={openViewer} />
              )}
            </Board>
          )}

          {/* BAND - the boss's own commitments: Calendar, Todos, Pursuits. One
              band under all three so the row reads as one area of the page. */}
          {(s.upcoming || s.todos || s.pursuits) && (
            <BoardBand>
              <Board>
                {s.upcoming && <UpcomingCard events={events} onOpen={openViewer} />}
                {s.todos && (
                  <TodosCard
                    todos={todos}
                    onOpen={openViewer}
                    onToggle={toggleTodo}
                    onAdd={() => setAddingTodo(true)}
                  />
                )}
                {s.pursuits && (
                  <PursuitsCard
                    pursuits={pursuits}
                    onOpen={openViewer}
                    onToggleHabit={toggleHabit}
                  />
                )}
              </Board>
            </BoardBand>
          )}

          {/* PLAIN - Agent work: the live picture of what Jarvis is doing
              right now (crons, plans, skills, …), as one grouped ledger. */}
          {s.work && <AgentWorkBoard items={work} onOpen={openViewer} />}

          {/* CARD - the one section that is a single object he acts on. */}
          {s.reflection && reflection && (
            <ReflectionCard reflection={reflection} onOpen={openViewer} />
          )}

          {/* BAND */}
          {s.saved && (
            <SectionBand>
              <SavedCard
                saved={saved}
                artifacts={artifacts}
                onOpen={openViewer}
              />
            </SectionBand>
          )}

          {/* PLAIN */}
          {s.activity && <ActivityCard activity={activity} onOpen={openViewer} />}
          </>
          )}
        </main>

        {s.memoryFooter && <MemoryFooter stats={memoryStats} />}
      </div>

      <ObjectViewer item={liveViewing} onClose={closeViewer} onResolved={resolveViewerItem} />

      {/* A search hit the dashboard does not already hold, opened in place.
          Same sheet the ⌘K palette uses, so one hit behaves one way. */}
      {recordSheet.sheet}

      {pcPursuit ? (
        <PCCockpit
          pursuitId={pcPursuit.id}
          title={pcPursuit.title}
          open
          onOpenChange={(next) => {
            if (!next) setPcPursuit(null);
            // Closing the cockpit may have advanced the programme (a logged
            // session, a taken proof), so pull the dashboard back in step.
            if (!next) void load({ background: true });
          }}
        />
      ) : null}

      {jhPursuit ? (
        <JobHuntCockpit
          pursuitId={jhPursuit.id}
          title={jhPursuit.title}
          open
          onOpenChange={(next) => {
            if (!next) setJhPursuit(null);
            // Closing the cockpit may have moved a role between stages, so
            // pull the dashboard back in step.
            if (!next) void load({ background: true });
          }}
        />
      ) : null}

      <AddTodoModal
        open={addingTodo}
        onOpenChange={setAddingTodo}
        onCreated={() => void load({ background: true })}
      />
    </AppShell>
  );
}
