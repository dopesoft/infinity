"use client";

import { useEffect, useMemo, useState } from "react";
import { IconRefresh, IconSearch } from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { MetricCard } from "@/components/MetricCard";
import { MemoryCard } from "@/components/MemoryCard";
import { MemoryDetail } from "@/components/MemoryDetail";
import {
  fetchMemoryCounts,
  fetchObservations,
  searchMemory,
  type MemoryCounts,
  type ObservationDTO,
  type SearchResult,
} from "@/lib/api";

type ListItem = ObservationDTO | SearchResult;

export default function MemoryPage() {
  const [counts, setCounts] = useState<MemoryCounts | null>(null);
  const [items, setItems] = useState<ListItem[]>([]);
  const [selected, setSelected] = useState<ListItem | null>(null);
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [loading, setLoading] = useState(true);
  const [showDetail, setShowDetail] = useState(false); // mobile drill-in

  async function loadDefault() {
    setLoading(true);
    const [c, obs] = await Promise.all([fetchMemoryCounts(), fetchObservations()]);
    setCounts(c);
    setItems(obs ?? []);
    setLoading(false);
  }

  useEffect(() => {
    loadDefault();
  }, []);

  async function runSearch(q: string) {
    if (!q.trim()) {
      loadDefault();
      return;
    }
    setSearching(true);
    const results = await searchMemory(q.trim());
    setItems(results ?? []);
    setSearching(false);
  }

  const filteredCount = useMemo(() => items.length, [items]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <MetricCard label="observations" value={counts?.observations ?? "—"} />
            <MetricCard label="memories" value={counts?.memories ?? "—"} />
            <MetricCard label="graph nodes" value={counts?.graph_nodes ?? "—"} />
            <MetricCard
              label="stale"
              value={counts?.stale ?? 0}
              highlight={(counts?.stale ?? 0) > 0}
            />
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              runSearch(query);
            }}
            className="flex items-center gap-2"
          >
            <div className="relative flex-1">
              <IconSearch
                className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden
              />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search memory (BM25 + vector + graph, RRF k=60)"
                className="pl-9"
                inputMode="search"
                enterKeyHint="search"
              />
            </div>
            <Button type="submit" size="default" disabled={searching}>
              {searching ? "…" : "search"}
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={loadDefault}
              aria-label="Refresh recent observations"
              disabled={loading}
            >
              <IconRefresh className="size-4" />
            </Button>
          </form>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          <aside
            className={`min-h-0 ${
              showDetail ? "hidden lg:flex" : "flex"
            } flex-1 flex-col overflow-y-auto border-b bg-background scroll-touch lg:w-80 lg:border-b-0 lg:border-r`}
          >
            <div className="flex items-center justify-between gap-2 px-3 pb-1 pt-3 text-[11px] uppercase tracking-wide text-muted-foreground">
              <span>{query ? "results" : "recent observations"}</span>
              <span>{filteredCount}</span>
            </div>
            <div className="flex flex-col gap-2 px-3 pb-4">
              {items.length === 0 ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {loading
                    ? "Loading…"
                    : query
                      ? "No results. Try a different query."
                      : "No observations yet — open Live and chat with Infinity to populate memory."}
                </p>
              ) : (
                items.map((it, i) => {
                  const id = "observation_id" in it ? it.observation_id : it.id;
                  return (
                    <MemoryCard
                      key={id + i}
                      source={it}
                      active={selectedId(selected) === id}
                      onClick={() => {
                        setSelected(it);
                        setShowDetail(true);
                      }}
                    />
                  );
                })
              )}
            </div>
          </aside>

          <section
            className={`min-h-0 flex-1 ${
              showDetail ? "flex" : "hidden lg:flex"
            } flex-col bg-background`}
          >
            {showDetail && (
              <button
                onClick={() => setShowDetail(false)}
                className="border-b px-4 py-2 text-left text-xs text-muted-foreground lg:hidden"
              >
                ← back to list
              </button>
            )}
            <MemoryDetail source={selected} onClose={() => setShowDetail(false)} />
          </section>
        </div>
      </div>
    </TabFrame>
  );
}

function selectedId(item: ListItem | null): string | null {
  if (!item) return null;
  return "observation_id" in item ? item.observation_id : item.id;
}
