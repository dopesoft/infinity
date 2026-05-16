"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { RefreshCw, Search } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/search-input";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
} from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import { fetchTraces, type TraceStatus, type TurnRowDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { TurnRow } from "@/components/logs/TurnRow";

/* /logs — LangSmith-style turn-by-turn list.
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

export default function LogsPage() {
  const [turns, setTurns] = useState<TurnRowDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [query, setQuery] = useState("");

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

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          {/* No "logs (#)" header — the total moves into the tab chips below
              so each status tab carries its own filter-aware count.
              Mirrors /memory + /skills. */}

          {/* Search row centered on desktop with a sane max width — mirrors
              Memory + Skills so the three tabs read as the same family.
              Mobile stays full-width. Reload sits to the right as an
              icon-only ghost button on mobile and a labeled primary
              button on desktop. */}
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
                placeholder="Search prompts, replies, sessions…"
              />
            </div>
            <Button
              type="button"
              onClick={() => void load()}
              disabled={loading}
              aria-label="Reload"
              title="Reload"
              className="h-9 w-9 shrink-0 bg-transparent px-0 text-foreground hover:bg-accent hover:text-foreground sm:w-auto sm:gap-1.5 sm:bg-primary sm:px-4 sm:text-primary-foreground sm:hover:bg-primary/90"
            >
              <RefreshCw className={cn("size-4 sm:hidden", loading && "animate-spin")} aria-hidden />
              <RefreshCw className={cn("hidden size-4 sm:inline-block", loading && "animate-spin")} aria-hidden />
              <span className="hidden sm:inline">{loading ? "…" : "Reload"}</span>
            </Button>
          </form>

          <div className="space-y-3">
            <PageTabs
              value={statusFilter}
              onValueChange={(v) => setStatusFilter(v as StatusFilter)}
              className="w-full"
            >
              <PageTabsList columns={6}>
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
        </div>

        {/* List area — matches /code-proposals padding rhythm exactly:
            px-3 py-3 sm:px-4, with the list inside on space-y-3.
            overflow-x-hidden as a mobile guardrail so a runaway token
            string in user_text can't push the column wider than 375px. */}
        <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-3 py-3 scroll-touch sm:px-4">
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
        </div>
      </div>
    </TabFrame>
  );
}
