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
  // True while a fetch for the current filter is outstanding — on first
  // load and again whenever the filter changes (a session switch). Realtime
  // + manual refreshes keep the snapshot fresh without re-toggling loading.
  loading: boolean;
  // Force a re-fetch. Most callers don't need this - realtime handles
  // updates automatically - but it's available for "pull to refresh"
  // or explicit retry buttons.
  refresh: () => void;
};

export type UseRunsOpts = FetchRunsOpts & {
  // When false the hook holds an empty snapshot and issues no request.
  // This is what a session-scoped consumer passes while its session id is
  // still empty: `fetchRuns` drops a falsy targetId from the query string,
  // so an un-gated call would silently widen the filter to EVERY session's
  // runs. Absent filter must never mean "show everything".
  enabled?: boolean;
};

export function useRuns(opts: UseRunsOpts = {}): UseRunsResult {
  const { enabled = true, ...filter } = opts;
  const [runs, setRuns] = useState<RunDTO[]>([]);
  const [loading, setLoading] = useState(true);
  // Keep the filter in a ref so the realtime subscription doesn't churn on
  // every render. Consumers typically pass an inline object literal,
  // which would otherwise trigger a re-subscription each render.
  const filterRef = useRef(filter);
  filterRef.current = filter;
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;
  // Monotonic request token: a response for a superseded filter must never
  // overwrite the snapshot of the current one.
  const seqRef = useRef(0);

  const reload = useCallback(async () => {
    const seq = seqRef.current;
    if (!enabledRef.current) {
      setRuns([]);
      setLoading(false);
      return;
    }
    const next = await fetchRuns(filterRef.current);
    if (seq !== seqRef.current) return; // filter moved on; drop the stale answer
    setRuns(next);
    setLoading(false);
  }, []);

  useEffect(() => {
    // The filter changed, so the snapshot in hand answers a different question.
    // Drop it rather than render another session's runs until the fetch lands.
    seqRef.current++;
    setRuns([]);
    setLoading(true);
    void reload();
    // The dependency uses a JSON.stringify so inline literals
    // don't trigger spurious reloads when the shape is identical.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(filter), enabled, reload]);

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
