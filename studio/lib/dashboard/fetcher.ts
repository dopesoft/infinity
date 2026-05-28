"use client";

import { authedFetch } from "@/lib/api";
import type {
  ActivityEvent,
  Approval,
  CalendarEvent,
  FollowUp,
  MemoryStats,
  Pursuit,
  Reflection,
  Saved,
  SurfaceItem,
  Todo,
  WorkItem,
} from "./types";

/* Dashboard fetcher.
 *
 * Wraps GET /api/dashboard. The endpoint returns sections that are
 * backed by real tables (pursuits/todos/calendar/followups/saved/memory
 * stats) - sections still wired to mock-only sources (reflection,
 * approvals, agent work, activity) are omitted from the response and
 * the DashboardClient keeps using its local fixture for those.
 *
 * Returns `null` on any failure so DashboardClient can fall back to
 * the full mock fixture without a thrown error.
 */
export type DashboardResponse = {
  pursuits: Pursuit[] | null;
  todos: Todo[] | null;
  calendarEvents: CalendarEvent[] | null;
  followUps: FollowUp[] | null;
  saved: Saved[] | null;
  approvals: Approval[] | null;
  reflection: Reflection | null;
  activity: ActivityEvent[] | null;
  work: WorkItem[] | null;
  memoryStats: MemoryStats | null;
  // Generic surface contract: items grouped by `surface` key.
  surfaceItems: Record<string, SurfaceItem[]> | null;
};

type RawResponse = {
  pursuits?: Array<
    Omit<Pursuit, "progress"> & {
      currentValue?: number | null;
      targetValue?: number | null;
      unit?: string | null;
    }
  > | null;
  todos?: Todo[] | null;
  calendarEvents?: CalendarEvent[] | null;
  followUps?: Array<FollowUp & { from?: string; from_name?: string }> | null;
  saved?: Saved[] | null;
  approvals?: Approval[] | null;
  reflection?: Reflection | null;
  activity?: ActivityEvent[] | null;
  work?: WorkItem[] | null;
  memoryStats?: MemoryStats | null;
  surfaceItems?: Record<string, SurfaceItem[]> | null;
};

// Stale-while-revalidate cache. The dashboard renders instantly from the
// last-known payload (localStorage) on mount, then fetchDashboard refreshes
// in the background. Modern-app feel: no blank-cards-for-5-seconds cold
// start. Bump the version suffix if DashboardResponse's shape changes.
const DASHBOARD_CACHE_KEY = "infinity:dashboard:v1";

export function readDashboardCache(): DashboardResponse | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(DASHBOARD_CACHE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as DashboardResponse;
  } catch {
    return null;
  }
}

function writeDashboardCache(data: DashboardResponse): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(DASHBOARD_CACHE_KEY, JSON.stringify(data));
  } catch {
    /* quota / private mode - cache is best-effort */
  }
}

export async function fetchDashboard(signal?: AbortSignal): Promise<DashboardResponse | null> {
  try {
    const res = await authedFetch("/api/dashboard", { signal });
    if (!res.ok) return null;
    const raw = (await res.json()) as RawResponse;
    const data: DashboardResponse = {
      pursuits: raw.pursuits ? raw.pursuits.map(mapPursuit) : null,
      todos: raw.todos ?? null,
      calendarEvents: raw.calendarEvents ?? null,
      followUps: raw.followUps ? raw.followUps.map(mapFollowUp) : null,
      saved: raw.saved ?? null,
      approvals: raw.approvals ?? null,
      reflection: raw.reflection ?? null,
      activity: raw.activity ?? null,
      work: raw.work ?? null,
      memoryStats: raw.memoryStats ?? null,
      surfaceItems: raw.surfaceItems ?? null,
    };
    writeDashboardCache(data);
    return data;
  } catch {
    return null;
  }
}

// Core's Pursuit DTO carries flat currentValue/targetValue/unit because
// JSONB → Go struct → JSON. The dashboard Pursuit type wants those
// nested under `progress` for habits-without-progress to not carry a
// stub object. Pull the structured shape together here.
function mapPursuit(
  p: Pick<Pursuit, "id" | "title" | "cadence" | "doneToday" | "doneAt" | "streakDays" | "dueAt" | "status" | "createdAt"> & {
    currentValue?: number | null;
    targetValue?: number | null;
    unit?: string | null;
  },
): Pursuit {
  const { currentValue, targetValue, unit, ...rest } = p;
  if (currentValue != null && targetValue != null) {
    return {
      ...rest,
      progress: { current: currentValue, target: targetValue, unit: unit ?? undefined },
    };
  }
  return { ...rest };
}

// Core sends `from_name` (Postgres column) but the FE type uses `from`.
// Normalize here so the rest of Studio stays clean.
function mapFollowUp(f: FollowUp & { from_name?: string }): FollowUp {
  if (f.from_name && !f.from) {
    return { ...f, from: f.from_name };
  }
  return f;
}
