"use client";

import { useEffect, useMemo, useState } from "react";
import { Brain, Eye, Search, SearchX, Sparkles, Zap } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { SearchInput } from "@/components/ui/search-input";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
  HScrollRow,
  FilterPill,
  PageSectionHeader,
} from "@/components/ui/page-tabs";
import { MetricCard } from "@/components/MetricCard";
import { MemoryCard } from "@/components/MemoryCard";
import { MemoryDetail } from "@/components/MemoryDetail";
import { BossProfilePanel } from "@/components/BossProfilePanel";
import { KnowledgeGraphPanel } from "@/components/KnowledgeGraphPanel";
import { useRealtime } from "@/lib/realtime/provider";
import { cn } from "@/lib/utils";
import {
  fetchMemories,
  fetchMemoryCounts,
  fetchObservations,
  fetchPredictions,
  fetchReflections,
  searchMemory,
  type MemoryCounts,
  type MemoryDTO,
  type ObservationDTO,
  type PredictionDTO,
  type ReflectionDTO,
  type SearchResult,
} from "@/lib/api";

type ListItem = ObservationDTO | SearchResult | MemoryDTO;

const TIERS = ["all", "working", "episodic", "semantic", "procedural"] as const;
type TierFilter = (typeof TIERS)[number];

const VIEWS = ["memories", "observations", "reflections", "predictions", "graph"] as const;
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
  const [reflections, setReflections] = useState<ReflectionDTO[]>([]);
  const [predictions, setPredictions] = useState<PredictionDTO[]>([]);

  async function loadDefault(viewArg: View = view, tierArg: TierFilter = tier) {
    setLoading(true);
    setQuery("");
    const c = await fetchMemoryCounts();
    setCounts(c);
    setReflections([]);
    setPredictions([]);
    if (viewArg === "graph") {
      // Graph view manages its own data fetch in KnowledgeGraphPanel.
      setItems([]);
    } else if (viewArg === "memories") {
      const mems = await fetchMemories(tierArg !== "all" ? { tier: tierArg } : {});
      setItems(mems ?? []);
    } else if (viewArg === "observations") {
      const obs = await fetchObservations();
      setItems(obs ?? []);
    } else if (viewArg === "reflections") {
      const rows = await fetchReflections();
      setItems([]);
      setReflections(rows ?? []);
    } else {
      const rows = await fetchPredictions();
      setItems([]);
      setPredictions(rows ?? []);
    }
    setLoading(false);
  }

  useEffect(() => {
    loadDefault();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Live updates: re-run the active list query whenever observations or
  // memories change, but only when the user isn't actively searching (a
  // realtime push during search would clobber their results).
  useRealtime(["mem_observations", "mem_memories", "mem_reflections", "mem_predictions"], () => {
    if (query.trim()) return;
    loadDefault();
  });

  async function runSearch(q: string) {
    if (!q.trim()) {
      loadDefault();
      return;
    }
    setSearching(true);
    const results = await searchMemory(q.trim());
    if (view !== "memories" && view !== "observations") {
      setView("memories");
    }
    setItems(results ?? []);
    setSearching(false);
  }

  const filteredCount = useMemo(() => {
    if (view === "reflections") return reflections.length;
    if (view === "predictions") return predictions.length;
    return items.length;
  }, [items.length, predictions.length, reflections.length, view]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          {/* Mobile: horizontal snap-scroll row. sm+: grid. The negative
              margins + edge padding let cards scroll flush to the screen
              edge on mobile while keeping a clean inset on tablet/desktop. */}
          <div className="-mx-3 sm:mx-0">
            <div className="no-scrollbar flex snap-x snap-mandatory gap-2 overflow-x-auto scroll-touch px-3 pb-1 sm:grid sm:grid-cols-3 sm:gap-2 sm:overflow-visible sm:px-0 sm:pb-0 lg:grid-cols-5">
              <MetricCard
                label="observations"
                value={counts?.observations ?? "—"}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="memories"
                value={counts?.memories ?? "—"}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="graph nodes"
                value={counts?.graph_nodes ?? "—"}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="graph edges"
                value={counts?.graph_edges ?? "—"}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="stale"
                value={counts?.stale ?? 0}
                highlight={(counts?.stale ?? 0) > 0}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
            </div>
          </div>

          {/* Search row centered on desktop with a sane max width — full
              page-width search input reads as wasted real estate on a
              25"+ monitor and pushes the result list way down. Mobile
              stays full-width since there's no space to spare. */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              runSearch(query);
            }}
            className="mx-auto flex w-full items-center gap-5 sm:max-w-2xl sm:pt-4"
          >
            <div className="min-w-0 flex-1">
              <SearchInput
                value={query}
                onValueChange={setQuery}
                placeholder={
                  view === "memories"
                    ? "Ask your memory anything…"
                    : view === "observations"
                      ? "Search what Infinity has noticed…"
                      : view === "graph"
                        ? "Find an entity in the graph…"
                        : "Search memories while this feed stays browsable…"
                }
              />
            </div>
            <Button
              type="submit"
              disabled={searching}
              aria-label="Search"
              title="Search"
              className="h-9 w-9 shrink-0 bg-transparent px-0 text-foreground hover:bg-accent hover:text-foreground sm:w-auto sm:gap-1.5 sm:bg-primary sm:px-4 sm:text-primary-foreground sm:hover:bg-primary/90"
            >
              <Search className="size-4 sm:hidden" aria-hidden />
              <span className="hidden sm:inline">
                {searching ? "…" : "Search"}
              </span>
            </Button>
          </form>

          <div className="space-y-3">
            <PageTabs
              value={view}
              onValueChange={(v) => {
                const next = v as View;
                setView(next);
                loadDefault(next, tier);
              }}
              className="w-full"
            >
              <PageTabsList scrollable>
                {VIEWS.map((v) => {
                  const count =
                    v === "memories"
                      ? (counts?.memories ?? null)
                      : v === "observations"
                        ? (counts?.observations ?? null)
                        : v === "reflections"
                          ? reflections.length
                          : v === "predictions"
                            ? predictions.length
                            : (counts?.graph_nodes ?? null);
                  return (
                    <PageTabsTrigger key={v} value={v} className="gap-1">
                      <span>{tabLabel(v)}</span>
                      {typeof count === "number" && (
                        <span
                          className={cn(
                            "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                            view === v
                              ? "bg-foreground text-background"
                              : "bg-muted-foreground/15 text-muted-foreground",
                          )}
                          aria-label={`${count} total`}
                        >
                          {count}
                        </span>
                      )}
                    </PageTabsTrigger>
                  );
                })}
              </PageTabsList>
            </PageTabs>

            {view === "memories" && (
              <HScrollRow>
                {TIERS.map((t) => (
                  <FilterPill
                    key={t}
                    active={tier === t}
                    onClick={() => {
                      setTier(t);
                      loadDefault("memories", t);
                    }}
                  >
                    {t}
                  </FilterPill>
                ))}
              </HScrollRow>
            )}
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          {/* Left column: brain panels (boss profile + voyager candidates).
              Hidden on small screens unless detail is closed; collapses into
              the list view on mobile. Hidden entirely in graph view to give
              the canvas more room. */}
          {view !== "graph" && view !== "reflections" && view !== "predictions" && (
            <aside
              className={cn(
                "min-h-0 w-full shrink-0 space-y-3 overflow-y-auto border-b bg-background px-3 py-3 scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
                showDetail ? "hidden lg:block" : "block",
              )}
            >
              <BossProfilePanel />
            </aside>
          )}

          {view === "graph" && <KnowledgeGraphPanel />}

          {view !== "graph" && (
            <>
              <aside
                className={cn(
                  "min-h-0 flex-1 flex-col overflow-y-auto border-b bg-background scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
                  (view === "reflections" || view === "predictions")
                    ? "lg:w-full lg:border-r-0"
                    : showDetail
                      ? "hidden lg:flex"
                      : "flex",
                )}
              >
                <PageSectionHeader
                  title={query ? "results" : view}
                  count={filteredCount}
                  className="px-3 pb-1 pt-3"
                />
                <div className="flex flex-1 flex-col gap-2 px-3 pb-4">
                  {view === "reflections" ? (
                    reflections.length === 0 ? (
                      <EmptyState
                        icon={Sparkles}
                        title={loading ? "Loading…" : "No reflections yet"}
                        description="Run the reflection loop to turn finished sessions into critiques and lessons."
                      />
                    ) : (
                      reflections.map((it) => <ReflectionRow key={it.id} item={it} />)
                    )
                  ) : view === "predictions" ? (
                    predictions.length === 0 ? (
                      <EmptyState
                        icon={Zap}
                        title={loading ? "Loading…" : "No high-surprise predictions"}
                        description="Surprise rows appear when tool results differ sharply from the agent's expectation."
                      />
                    ) : (
                      predictions.map((it) => <PredictionRow key={it.id} item={it} />)
                    )
                  ) : items.length === 0 ? (
                    loading ? (
                      <p className="px-1 text-sm text-muted-foreground">Loading…</p>
                    ) : query ? (
                      <EmptyState
                        icon={SearchX}
                        title="No matches"
                        description={
                          <>
                            Nothing in {view} matches{" "}
                            <code className="rounded bg-muted px-1 font-mono text-[10px]">
                              {query}
                            </code>.
                            Try a broader phrase or switch tiers.
                          </>
                        }
                      />
                    ) : view === "memories" ? (
                      <EmptyState
                        icon={Brain}
                        title="No memories yet"
                        description="Memories form as Infinity compresses what it's observed about you. Keep chatting in Live and they'll start landing here."
                      />
                    ) : (
                      <EmptyState
                        icon={Eye}
                        title="Nothing observed yet"
                        description={
                          <>
                            Every message, tool call, and decision in{" "}
                            <span className="font-medium text-foreground">Live</span> is
                            captured here first. Start a conversation to seed the stream.
                          </>
                        }
                      />
                    )
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

              {view !== "reflections" && view !== "predictions" && (
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
              )}
            </>
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function ReflectionRow({ item }: { item: ReflectionDTO }) {
  return (
    <article className="rounded-xl border bg-card px-3 py-3">
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span className="inline-flex items-center gap-1 font-mono uppercase text-tier-procedural">
          <Sparkles className="size-3" aria-hidden />
          {item.kind || "reflection"}
        </span>
        <time dateTime={item.created_at} suppressHydrationWarning>
          {new Date(item.created_at).toLocaleString()}
        </time>
      </div>
      <p className="mt-2 line-clamp-4 break-words text-sm">{item.critique || "—"}</p>
      <div className="mt-2 flex flex-wrap gap-1 text-[10px]">
        <span className="rounded-full bg-tier-procedural/10 px-2 py-0.5 font-mono text-tier-procedural">
          quality {(item.quality_score * 100).toFixed(0)}%
        </span>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-muted-foreground">
          importance {item.importance}
        </span>
      </div>
      {item.lessons?.length > 0 && (
        <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
          {item.lessons.slice(0, 3).map((lesson, i) => (
            <li key={`${item.id}:${i}`} className="line-clamp-2">
              {lesson.text}
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}

function PredictionRow({ item }: { item: PredictionDTO }) {
  return (
    <article className="rounded-xl border bg-card px-3 py-3">
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span className="inline-flex min-w-0 items-center gap-1 font-mono uppercase text-warning">
          <Zap className="size-3 shrink-0" aria-hidden />
          <span className="truncate">{item.tool_name}</span>
        </span>
        <span className="font-mono text-warning">
          {(item.surprise_score * 100).toFixed(0)}%
        </span>
      </div>
      <p className="mt-2 line-clamp-2 break-words text-xs text-muted-foreground">
        Expected: <span className="text-foreground">{item.expected || "—"}</span>
      </p>
      <p className="mt-1 line-clamp-3 break-words text-xs text-muted-foreground">
        Actual: <span className="text-foreground">{item.actual || "unresolved"}</span>
      </p>
      <div className="mt-2 flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono">
          {item.matched ? "matched" : "surprised"}
        </span>
        <time dateTime={item.created_at} suppressHydrationWarning>
          {new Date(item.created_at).toLocaleString()}
        </time>
      </div>
    </article>
  );
}

function selectedId(item: ListItem | null): string | null {
  if (!item) return null;
  if ("observation_id" in item) return item.observation_id;
  return item.id;
}

function tabLabel(view: View): string {
  if (view === "observations") return "obs";
  if (view === "reflections") return "reflect";
  if (view === "predictions") return "predict";
  return view;
}
