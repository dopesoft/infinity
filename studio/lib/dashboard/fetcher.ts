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
  Todo,
} from "./types";

/* Dashboard fetcher.
 *
 * Wraps GET /api/dashboard. The endpoint returns sections that are
 * backed by real tables (pursuits/todos/calendar/followups/saved/memory
 * stats) — sections still wired to mock-only sources (reflection,
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
  memoryStats: MemoryStats | null;
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
  memoryStats?: MemoryStats | null;
};

export async function fetchDashboard(signal?: AbortSignal): Promise<DashboardResponse | null> {
  try {
    const res = await authedFetch("/api/dashboard", { signal });
    if (!res.ok) return null;
    const raw = (await res.json()) as RawResponse;
    return {
      pursuits: raw.pursuits ? raw.pursuits.map(mapPursuit) : null,
      todos: raw.todos ?? null,
      calendarEvents: raw.calendarEvents ?? null,
      followUps: raw.followUps ? raw.followUps.map(mapFollowUp) : null,
      saved: raw.saved ?? null,
      approvals: raw.approvals ?? null,
      reflection: raw.reflection ?? null,
      activity: raw.activity ?? null,
      memoryStats: raw.memoryStats ?? null,
    };
  } catch {
    return null;
  }
}

// Core's Pursuit DTO carries flat currentValue/targetValue/unit because
// JSONB → Go struct → JSON. The dashboard Pursuit type wants those
// nested under `progress` for habits-without-progress to not carry a
// stub object. Pull the structured shape together here.
function mapPursuit(
  p: Pick<Pursuit, "id" | "title" | "cadence" | "doneToday" | "doneAt" | "streakDays" | "dueAt" | "status"> & {
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
