"use client";

import { useEffect, useMemo, useState } from "react";
import { RefreshCw, Search } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { MetricCard } from "@/components/MetricCard";
import { MemoryCard } from "@/components/MemoryCard";
import { MemoryDetail } from "@/components/MemoryDetail";
import { BossProfilePanel } from "@/components/BossProfilePanel";
import { CandidateSkillsPanel } from "@/components/CandidateSkillsPanel";
import { KnowledgeGraphPanel } from "@/components/KnowledgeGraphPanel";
import { cn } from "@/lib/utils";
import {
  fetchMemories,
  fetchMemoryCounts,
  fetchObservations,
  searchMemory,
  type MemoryCounts,
  type MemoryDTO,
  type ObservationDTO,
  type SearchResult,
} from "@/lib/api";

type ListItem = ObservationDTO | SearchResult | MemoryDTO;

const TIERS = ["all", "working", "episodic", "semantic", "procedural"] as const;
type TierFilter = (typeof TIERS)[number];

const VIEWS = ["memories", "observations", "graph"] as const;
type View = (typeof VIEWS)[number];

export default function MemoryPage() {
  const [counts, setCounts] = useState<MemoryCounts | null>(null);
  const [items, setItems] = useState<ListItem[]>([]);
  const [selected, setSelected] = useState<ListItem | null>(null);
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [loading, setLoading] = useState(true);
  const [showDetail, setShowDetail] = useState(false);
  const [view, setView] = useState<View>("memories");
  const [tier, setTier] = useState<TierFilter>("all");

  async function loadDefault(viewArg: View = view, tierArg: TierFilter = tier) {
    setLoading(true);
    setQuery("");
    const c = await fetchMemoryCounts();
    setCounts(c);
    if (viewArg === "graph") {
      // Graph view manages its own data fetch in KnowledgeGraphPanel.
      setItems([]);
    } else if (viewArg === "memories") {
      const mems = await fetchMemories(tierArg !== "all" ? { tier: tierArg } : {});
      setItems(mems ?? []);
    } else {
      const obs = await fetchObservations();
      setItems(obs ?? []);
    }
    setLoading(false);
  }

  useEffect(() => {
    loadDefault();
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
            <MetricCard label="observations" value={counts?.observations ?? "—"} />
            <MetricCard label="memories" value={counts?.memories ?? "—"} />
            <MetricCard label="graph nodes" value={counts?.graph_nodes ?? "—"} />
            <MetricCard label="graph edges" value={counts?.graph_edges ?? "—"} />
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
              <Search
                className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden
              />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search (BM25 + vector + graph, RRF k=60)"
                className="pl-9"
                inputMode="search"
                enterKeyHint="search"
              />
            </div>
            <Button type="submit" disabled={searching}>
              {searching ? "…" : "search"}
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={() => loadDefault()}
              aria-label="Refresh"
              disabled={loading}
            >
              <RefreshCw className="size-4" />
            </Button>
          </form>

          <div className="flex flex-wrap items-center gap-1.5">
            <div className="flex items-center gap-1">
              {VIEWS.map((v) => (
                <button
                  key={v}
                  onClick={() => {
                    setView(v);
                    loadDefault(v, tier);
                  }}
                  className={cn(
                    "inline-flex min-h-9 items-center rounded-md border px-3 font-mono text-xs uppercase tracking-wide lg:min-h-7 lg:px-2",
                    view === v
                      ? "border-info bg-info/10 text-info"
                      : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
                  )}
                >
                  {v}
                </button>
              ))}
            </div>
            {view === "memories" && (
              <div className="flex flex-wrap items-center gap-1">
                {TIERS.map((t) => (
                  <button
                    key={t}
                    onClick={() => {
                      setTier(t);
                      loadDefault("memories", t);
                    }}
                    className={cn(
                      "inline-flex min-h-9 items-center rounded-full border px-3 font-mono text-[11px] uppercase tracking-wide lg:min-h-6 lg:px-2 lg:text-[10px]",
                      tier === t
                        ? "border-info bg-info/10 text-info"
                        : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
                    )}
                  >
                    {t}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          {/* Left column: brain panels (boss profile + voyager candidates).
              Hidden on small screens unless detail is closed; collapses into
              the list view on mobile. Hidden entirely in graph view to give
              the canvas more room. */}
          {view !== "graph" && (
            <aside
              className={cn(
                "min-h-0 w-full shrink-0 space-y-3 overflow-y-auto border-b bg-background px-3 py-3 scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
                showDetail ? "hidden lg:block" : "block",
              )}
            >
              <BossProfilePanel />
              <CandidateSkillsPanel />
            </aside>
          )}

          {view === "graph" && (
            <KnowledgeGraphPanel />
          )}

          {view !== "graph" && (
          <>
          <aside
            className={cn(
              "min-h-0 flex-1 flex-col overflow-y-auto border-b bg-background scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
              showDetail ? "hidden lg:flex" : "flex",
            )}
          >
            <div className="flex items-center justify-between gap-2 px-3 pb-1 pt-3 text-[11px] uppercase tracking-wide text-muted-foreground">
              <span>{query ? "results" : view}</span>
              <span>{filteredCount}</span>
            </div>
            <div className="flex flex-col gap-2 px-3 pb-4">
              {items.length === 0 ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {loading
                    ? "Loading…"
                    : query
                      ? "No results."
                      : view === "memories"
                        ? "No memories yet. Set INFINITY_AUTO_COMPRESS=true on Core, or use the remember tool, to create some."
                        : "No observations yet — open Live and chat with Infinity."}
                </p>
              ) : (
                items.map((it, i) => (
                  <MemoryCard
                    key={selectedId(it) + ":" + i}
                    source={it}
                    active={selectedId(selected) === selectedId(it)}
                    onClick={() => {
                      setSelected(it);
                      setShowDetail(true);
                    }}
                  />
                ))
              )}
            </div>
          </aside>

          <section
            className={cn(
              "min-h-0 flex-1 flex-col bg-background",
              showDetail ? "flex" : "hidden lg:flex",
            )}
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
          </>
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function selectedId(item: ListItem | null): string | null {
  if (!item) return null;
  if ("observation_id" in item) return item.observation_id;
  return item.id;
}
