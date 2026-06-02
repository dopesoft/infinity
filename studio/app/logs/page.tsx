"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { TabFrame } from "@/components/TabFrame";
import { SearchInput } from "@/components/ui/search-input";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
} from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import { fetchTraces, type TurnRowDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import { useRuns } from "@/lib/runs/useRuns";
import { TurnRow } from "@/components/logs/TurnRow";
import { RunRow } from "@/components/logs/RunRow";

/* /logs - LangSmith-style turn-by-turn list.
 *
 * Layout mirrors /memory and /skills exactly so the three pages read as the
 * same family: centered search form with reload sitting to the right as
 * an icon-only ghost button (mobile) / labeled primary button (desktop),
 * then status PageTabs with per-tab count chips. No bespoke widths, no
 * filter rows squatting in the header.
 */
const STATUS_FILTERS = [
  "all",
  "in_flight",
  "ok",
  "empty",
  "errored",
  "interrupted",
] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

const STATUS_LABELS: Record<StatusFilter, string> = {
  all: "all",
  in_flight: "running",
  ok: "ok",
  empty: "empty",
  errored: "errored",
  interrupted: "stopped",
};

const LENSES = ["turns", "runs"] as const;
type Lens = (typeof LENSES)[number];

export default function LogsPage() {
  const [turns, setTurns] = useState<TurnRowDTO[]>([]);
  const [loading, setLoading] = useState(true);
  // Lens persists in ?lens=<id>: "turns" = chat/agent turn traces (mem_turns),
  // "runs" = the durable record of every server-tracked action (mem_runs) -
  // where a dashboard "Done" card lives on after it ages out of today.
  const [lens, setLens] = useTabParam<Lens>("lens", "turns", LENSES);
  // Status tab persists in ?status=<id> so a refresh keeps the filter.
  const [statusFilter, setStatusFilter] = useTabParam<StatusFilter>(
    "status",
    "all",
    STATUS_FILTERS,
  );
  const [query, setQuery] = useState("");
  // mem_runs history - realtime + survives navigation/refresh via useRuns.
  const { runs, loading: runsLoading } = useRuns({ limit: 200 });

  const load = useCallback(async () => {
    setLoading(true);
    const rows = await fetchTraces({ limit: 200 });
    setTurns(rows ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime(["mem_turns", "mem_observations"], () => void load());

  const counts = useMemo(() => {
    const c: Record<StatusFilter, number> = {
      all: turns.length,
      in_flight: 0,
      ok: 0,
      empty: 0,
      errored: 0,
      interrupted: 0,
    };
    for (const t of turns) {
      if (t.status in c) c[t.status as StatusFilter]++;
    }
    return c;
  }, [turns]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    let rows = statusFilter === "all" ? turns : turns.filter((t) => t.status === statusFilter);
    if (q) {
      rows = rows.filter((t) =>
        [t.user_text, t.summary, t.session_name, t.assistant_text, t.model]
          .map((v) => (v ?? "").toLowerCase())
          .some((v) => v.includes(q)),
      );
    }
    return rows;
  }, [turns, statusFilter, query]);

  const filteredRuns = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return runs;
    return runs.filter((r) =>
      [r.label, r.kind, r.result_summary, r.target_id, r.human_error?.title]
        .map((v) => (v ?? "").toLowerCase())
        .some((v) => v.includes(q)),
    );
  }, [runs, query]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 px-4 py-5 sm:px-6 lg:px-8">
          {/* Lens toggle: chat/agent Turns vs the durable Runs record. */}
          <div className="mx-auto w-full sm:max-w-2xl">
            <PageTabs value={lens} onValueChange={(v) => setLens(v as Lens)} className="w-full">
              <PageTabsList scrollable>
                <PageTabsTrigger value="turns">Turns</PageTabsTrigger>
                <PageTabsTrigger value="runs" className="gap-1.5">
                  <span>Runs</span>
                  <span
                    className={cn(
                      "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                      lens === "runs"
                        ? "bg-foreground text-background"
                        : "bg-muted-foreground/15 text-muted-foreground",
                    )}
                  >
                    {runs.length}
                  </span>
                </PageTabsTrigger>
              </PageTabsList>
            </PageTabs>
          </div>

          {/* Search row centered on desktop with a sane max width - mirrors
              Memory + Skills so the three tabs read as the same family.
              Mobile stays full-width. Pull-to-refresh (mobile) and the
              browser refresh (desktop) handle re-fetch, so no inline
              reload button. */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void load();
            }}
            className="mx-auto flex w-full items-center gap-5 sm:max-w-2xl sm:pt-1"
          >
            <div className="min-w-0 flex-1">
              <SearchInput
                value={query}
                onValueChange={setQuery}
                placeholder={
                  lens === "runs"
                    ? "Search runs — nightly cognition, self-improve, skills…"
                    : "Search prompts, replies, sessions…"
                }
              />
            </div>
          </form>

          {lens === "turns" ? (
            <div className="space-y-3">
              <PageTabs
                value={statusFilter}
                onValueChange={(v) => setStatusFilter(v as StatusFilter)}
                className="w-full"
              >
                <PageTabsList scrollable>
                  {STATUS_FILTERS.map((s) => (
                    <PageTabsTrigger key={s} value={s} className="gap-1.5">
                      <span>{STATUS_LABELS[s]}</span>
                      <span
                        className={cn(
                          "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                          statusFilter === s
                            ? "bg-foreground text-background"
                            : "bg-muted-foreground/15 text-muted-foreground",
                        )}
                        aria-label={`${counts[s]} matching`}
                      >
                        {counts[s]}
                      </span>
                    </PageTabsTrigger>
                  ))}
                </PageTabsList>
              </PageTabs>
            </div>
          ) : null}
        </div>

        {/* List area - matches /code-proposals padding rhythm exactly:
            px-3 py-3 sm:px-4, with the list inside on space-y-3.
            overflow-x-hidden as a mobile guardrail so a runaway token
            string in user_text can't push the column wider than 375px. */}
        <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-3 py-3 scroll-touch sm:px-4">
          {lens === "turns" ? (
            <>
              {!loading && filtered.length === 0 && (
                <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
                  {query.trim() || statusFilter !== "all"
                    ? "No turns match."
                    : "No turns captured yet. Send a message in Live and one will land here."}
                </div>
              )}
              <ul className="space-y-3">
                {filtered.map((t) => (
                  <li key={t.id} className="min-w-0">
                    <TurnRow turn={t} />
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <>
              {!runsLoading && filteredRuns.length === 0 && (
                <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
                  {query.trim()
                    ? "No runs match."
                    : "No runs recorded yet. Scheduled work, skills, and heartbeat scans land here as they fire."}
                </div>
              )}
              <ul className="space-y-3">
                {filteredRuns.map((r) => (
                  <li key={r.id} className="min-w-0">
                    <RunRow run={r} />
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      </div>
    </TabFrame>
  );
}
