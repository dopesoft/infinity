"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { TabFrame } from "@/components/TabFrame";
import { DashboardHeader } from "./DashboardHeader";
import { PursuitsCard } from "./PursuitsCard";
import { TodosCard } from "./TodosCard";
import { UpcomingCard } from "./UpcomingCard";
import { ReflectionCard } from "./ReflectionCard";
import { ApprovalsCard } from "./ApprovalsCard";
import { FollowUpsCard } from "./FollowUpsCard";
import { SurfaceCard } from "./SurfaceCard";
import { AgentWorkBoard } from "./AgentWorkBoard";
import { SavedCard } from "./SavedCard";
import { ActivityCard } from "./ActivityCard";
import { MemoryFooter } from "./MemoryFooter";
import { ObjectViewer } from "./ObjectViewer";
import { useDashboardPrefs } from "@/lib/dashboard/preferences";
import { fetchDashboard } from "@/lib/dashboard/fetcher";
import type {
  ActivityEvent,
  Approval,
  CalendarEvent,
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

/* DashboardClient — the orchestrating client component for the Dashboard
 * tab. Holds local mock state (toggle habits/todos optimistically),
 * routes taps into the ObjectViewer, and lays out every section.
 *
 * Layout rules:
 *   • Mobile (<lg): single column scroll, sections stacked top-to-bottom.
 *   • Desktop (lg+): TODAY row is 3-column (Pursuits | Todos | Upcoming),
 *     then Reflection full-width, then Approvals | Follow-ups 2-col,
 *     then Agent Work + Saved + Activity each full-width.
 *
 * Search filters across every section's content. When active, sections
 * with zero matches are still rendered so the page structure stays
 * stable — they just say "no matches" inline.
 */

const ZERO_MEMORY_STATS: MemoryStats = {
  newToday: 0,
  promotedToday: 0,
  procedural: 0,
  streakDays: 0,
};

export function DashboardClient() {
  // Every section starts empty and is filled only by /api/dashboard.
  // No mock fixtures, no fallback fixtures — if the fetch fails the
  // dashboard shows empty state, not a lie.
  const [pursuits, setPursuits] = useState<Pursuit[]>([]);
  const [todos, setTodos] = useState<Todo[]>([]);
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [followUps, setFollowUps] = useState<FollowUp[]>([]);
  const [work, setWork] = useState<WorkItem[]>([]);
  const [saved, setSaved] = useState<Saved[]>([]);
  const [activity, setActivity] = useState<ActivityEvent[]>([]);
  const [reflection, setReflection] = useState<Reflection | null>(null);
  const [memoryStats, setMemoryStats] = useState<MemoryStats>(ZERO_MEMORY_STATS);
  // Generic surface contract: items the agent surfaced via `surface_item`,
  // grouped by `surface` key. A new surface the agent invents renders here
  // automatically — no new state field, no new card component.
  const [surfaceItems, setSurfaceItems] = useState<Record<string, SurfaceItem[]>>({});

  useEffect(() => {
    const ctl = new AbortController();
    void (async () => {
      const data = await fetchDashboard(ctl.signal);
      if (!data) return;
      setPursuits(data.pursuits ?? []);
      setTodos(data.todos ?? []);
      setEvents(data.calendarEvents ?? []);
      setFollowUps(data.followUps ?? []);
      setSaved(data.saved ?? []);
      setApprovals(data.approvals ?? []);
      setActivity(data.activity ?? []);
      setWork(data.work ?? []);
      setReflection(data.reflection ?? null);
      setSurfaceItems(data.surfaceItems ?? {});
      if (data.memoryStats) setMemoryStats(data.memoryStats);
    })();
    return () => ctl.abort();
  }, []);

  const [search, setSearch] = useState("");
  const [viewing, setViewing] = useState<DashboardItem | null>(null);
  const { prefs } = useDashboardPrefs();
  const s = prefs.sections;

  const openViewer = useCallback((item: DashboardItem) => setViewing(item), []);
  const closeViewer = useCallback(() => setViewing(null), []);
  const resolveViewerItem = useCallback((item: DashboardItem) => {
    if (item.kind !== "approval") return;
    const id = item.data.id;
    setApprovals((prev) => prev.filter((a) => a.id !== id));
    setWork((prev) =>
      prev.filter((w) => w.id !== `trust-${id}` && w.id !== `code-${id}`),
    );
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

  const toggleTodo = useCallback((id: string) => {
    setTodos((prev) => prev.map((t) => (t.id === id ? { ...t, done: !t.done } : t)));
  }, []);

  // Lightweight client-side search. Each section gets a pre-filtered
  // slice based on the same query, applied across the most-relevant
  // textual fields per kind.
  const q = search.trim().toLowerCase();
  const filtered = useMemo(() => {
    if (!q) {
      return {
        pursuits,
        todos,
        events,
        approvals,
        followUps,
        work,
        saved,
        activity,
        surfaceItems,
      };
    }
    const match = (...parts: (string | undefined | null)[]) =>
      parts.some((p) => (p ?? "").toLowerCase().includes(q));
    // Surface groups filter per-item; a group with zero matches drops out.
    const surfaceFiltered: Record<string, SurfaceItem[]> = {};
    for (const [key, items] of Object.entries(surfaceItems)) {
      const m = items.filter((it) =>
        match(it.title, it.subtitle, it.body, it.kind, it.source),
      );
      if (m.length) surfaceFiltered[key] = m;
    }
    return {
      pursuits: pursuits.filter((p) => match(p.title, p.cadence)),
      todos: todos.filter((t) => match(t.title, t.priority, t.source)),
      events: events.filter((e) =>
        match(e.title, e.classification, e.location, ...e.prep.map((p) => p.label)),
      ),
      approvals: approvals.filter((a) =>
        match(a.title, a.subtitle, a.rationale, a.question),
      ),
      followUps: followUps.filter((f) =>
        match(f.from, f.subject, f.preview, f.body, f.account),
      ),
      work: work.filter((w) => match(w.title, w.subtitle, w.kind)),
      saved: saved.filter((s) => match(s.title, s.body, s.source, s.url)),
      activity: activity.filter((e) => match(e.title, e.detail)),
      surfaceItems: surfaceFiltered,
    };
  }, [q, pursuits, todos, events, approvals, followUps, work, saved, activity, surfaceItems]);

  // Counter for the "need you" badge in the header — anything actionable.
  // High-importance surfaced items (80+) count too.
  const needYouCount =
    approvals.length +
    followUps.filter((f) => f.unread).length +
    Object.values(surfaceItems)
      .flat()
      .filter((it) => (it.importance ?? 0) >= 80).length;

  return (
    <TabFrame>
      {/* min-w-0 + overflow-x-hidden keep the dashboard page-locked on
          mobile: any card with wider-than-viewport content (heartbeat
          artifact JSON, long titles, etc.) gets clipped at the page
          edge instead of pushing the whole page horizontally and
          breaking the fixed-composer / fixed-header invariant the rest
          of the app relies on. */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overflow-x-hidden scroll-touch">
        <DashboardHeader badgeCount={needYouCount} search={search} onSearchChange={setSearch} />

        <main className="mx-auto w-full min-w-0 max-w-6xl flex-1 space-y-5 px-3 pb-2 sm:px-4 sm:space-y-6">
          {/* TODAY row — collapses to fewer columns if any sub-section is off. */}
          {(s.pursuits || s.todos || s.upcoming) && (
            <div className="grid gap-4 sm:gap-5 lg:grid-cols-3">
              {s.pursuits && (
                <PursuitsCard
                  pursuits={filtered.pursuits}
                  onOpen={openViewer}
                  onToggleHabit={toggleHabit}
                />
              )}
              {s.todos && (
                <TodosCard todos={filtered.todos} onOpen={openViewer} onToggle={toggleTodo} />
              )}
              {s.upcoming && <UpcomingCard events={filtered.events} onOpen={openViewer} />}
            </div>
          )}

          {/* Generic surface contract — every group the agent surfaced via
              `surface_item`, each rendered by one generic SurfaceCard. A new
              surface the agent invents appears here with zero new code. */}
          {Object.keys(filtered.surfaceItems).length > 0 && (
            <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
              {Object.entries(filtered.surfaceItems).map(([surfaceKey, items], i) => (
                <SurfaceCard
                  key={surfaceKey}
                  surface={surfaceKey}
                  items={items}
                  delay={0.15 + i * 0.05}
                  onOpen={openViewer}
                />
              ))}
            </div>
          )}

          {s.reflection && reflection && (
            <ReflectionCard reflection={reflection} onOpen={openViewer} />
          )}

          {(s.approvals || s.followups) && (
            <div className="grid gap-4 sm:gap-5 lg:grid-cols-2">
              {s.approvals && (
                <ApprovalsCard approvals={filtered.approvals} onOpen={openViewer} />
              )}
              {s.followups && (
                <FollowUpsCard followUps={filtered.followUps} onOpen={openViewer} />
              )}
            </div>
          )}

          {s.work && <AgentWorkBoard items={filtered.work} onOpen={openViewer} />}

          {s.saved && <SavedCard saved={filtered.saved} onOpen={openViewer} />}

          {s.activity && <ActivityCard activity={filtered.activity} onOpen={openViewer} />}
        </main>

        {s.memoryFooter && <MemoryFooter stats={memoryStats} />}
      </div>

      <ObjectViewer item={viewing} onClose={closeViewer} onResolved={resolveViewerItem} />
    </TabFrame>
  );
}
