"use client";

// useRuns is the canonical Studio hook for reading server-tracked
// long-action progress (mem_runs). It is the consumer side of the
// "Server-tracked progress" rule in CLAUDE.md - every long server
// action (cron, skill, heartbeat, voyager.optimize, gym.extract, …)
// books a mem_runs row via runs.Track in Go, and Studio reads via this
// hook so the spinner survives:
//
//   - route navigation in the SPA
//   - browser tab switch / window backgrounding
//   - browser refresh
//   - device switch (a second device opening the same screen sees the
//     same in-flight state because both read from the DB)
//
// On mount: HTTP GET /api/runs to backfill recent + in-flight rows so
// the UI has truth even when no realtime event fires (eg. the run
// started before the page was opened). Then subscribe to realtime
// updates on mem_runs so future INSERT / UPDATE / DELETE events refresh
// the local snapshot. Pass {kind, targetId} to filter down to "this
// specific row's runs" (the common "is THIS cron running?" question).
//
// The hook returns the most recent run for the filter PLUS the full list,
// because the most common UI shape is "show a spinner if status=running,
// else show the last result." Consumers that need every recent run
// (history view) read the `runs` array.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRealtime } from "@/lib/realtime/provider";
import { fetchRuns, type RunDTO, type FetchRunsOpts } from "@/lib/api";

export type UseRunsResult = {
  // The most recent run matching the filter, or null when none.
  // Status === 'running' means "show a spinner." status === 'error'
  // means "show the error message." status === 'ok' means "show the
  // last-run summary." This is the field 95% of consumers want.
  latest: RunDTO | null;
  // All matching runs ordered by started_at DESC (most recent first).
  // Read this for history-style views.
  runs: RunDTO[];
  // True for the very first load only. After that, realtime + manual
  // refreshes keep the snapshot fresh without re-toggling loading.
  loading: boolean;
  // Force a re-fetch. Most callers don't need this - realtime handles
  // updates automatically - but it's available for "pull to refresh"
  // or explicit retry buttons.
  refresh: () => void;
};

export function useRuns(opts: FetchRunsOpts = {}): UseRunsResult {
  const [runs, setRuns] = useState<RunDTO[]>([]);
  const [loading, setLoading] = useState(true);
  // Keep opts in a ref so the realtime subscription doesn't churn on
  // every render. Consumers typically pass an inline object literal,
  // which would otherwise trigger a re-subscription each render.
  const optsRef = useRef(opts);
  optsRef.current = opts;

  const reload = useCallback(async () => {
    const next = await fetchRuns(optsRef.current);
    setRuns(next);
    setLoading(false);
  }, []);

  useEffect(() => {
    void reload();
    // The opts dependency uses a JSON.stringify so inline literals
    // don't trigger spurious reloads when the shape is identical.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(opts)]);

  // Realtime: any change to mem_runs re-fetches the filtered snapshot.
  // This is intentionally simple - the payload may not match the
  // filter, and re-fetching is cheap (small index-only scan). If this
  // ever becomes a hot path, switch to in-payload filtering.
  useRealtime("mem_runs", () => {
    void reload();
  });

  const latest = useMemo(() => (runs.length > 0 ? runs[0] : null), [runs]);

  return { latest, runs, loading, refresh: reload };
}
