"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { fetchActivePlan } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import type { Plan } from "@/lib/dashboard/types";

/* usePlan - the active/paused plan for a chat session, live.
 *
 * Reads GET /api/plans/active on mount + whenever mem_plans / mem_plan_steps
 * change (realtime, debounced), so the chat dock renders exactly the same plan
 * the dashboard Agent Work board shows - one substrate, two synced views. Like
 * useRuns, it reads server state so it survives navigation, refresh, focus
 * loss, and a second device. Returns null when nothing is in flight. */
export function usePlan(sessionId: string | undefined): Plan | null {
  const [plan, setPlan] = useState<Plan | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      if (!sessionId) {
        setPlan(null);
        return;
      }
      const p = await fetchActivePlan(sessionId, signal);
      setPlan(p);
    },
    [sessionId],
  );

  useEffect(() => {
    const ctl = new AbortController();
    void load(ctl.signal);
    return () => ctl.abort();
  }, [load]);

  // Realtime: any plan/step change → debounced refetch of this session's plan.
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const debounced = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => void load(), 400);
  }, [load]);
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
  useRealtime(["mem_plans", "mem_plan_steps"], debounced);

  return plan;
}
