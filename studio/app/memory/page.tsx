"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Brain, Eye, Sparkles, Zap } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { EmptyState } from "@/components/EmptyState";
import { CountLine } from "@/components/ui/count-line";
import { SearchPage } from "@/components/ui/search-page";
import { GroupLabel } from "@/components/ui/list-row";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { MemoryCard } from "@/components/MemoryCard";
import { MemoryDetail } from "@/components/MemoryDetail";
import { BossProfilePanel } from "@/components/BossProfilePanel";
import { CompassSection } from "@/components/settings/CompassSection";
import { KnowledgeGraphPanel } from "@/components/KnowledgeGraphPanel";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import {
  fetchMemories,
  fetchMemoryCounts,
  fetchObservations,
  fetchPredictions,
  fetchReflectionChains,
  fetchReflections,
  searchMemory,
  type MemoryCounts,
  type MemoryDTO,
  type ObservationDTO,
  type PredictionDTO,
  type ReflectionChainDTO,
  type ReflectionDTO,
  type SearchResult,
} from "@/lib/api";

type ListItem = ObservationDTO | SearchResult | MemoryDTO;

/**
 * Memory — search first, because nobody browses twelve thousand facts.
 *
 * WHAT CHANGED, AND WHY
 *
 * This page used to open as a row of five metric tiles, a search form with
 * its own submit button, a six-tab strip labelled `obs / reflect / predict`,
 * a tier chip row, and a three-column list-plus-detail layout. Six controls
 * before a single fact, and half of them named after storage tiers.
 *
 * The real question you arrive with is "does he know this, and where did he
 * get it". That is a search, so the page IS the field. What sits on it
 * before you type is only the two things worth showing unprompted: what he
 * knows about you, and what he learned today.
 *
 * The counts under the field are a <CountLine>, not five more chips — these
 * are different KINDS (a fact, a lesson, a wrong guess, a raw observation),
 * and a chip row reads as five buttons of equal weight with the number you
 * came for buried inside a pill.
 *
 * Compass moved IN from Settings as "About you": your mission is a fact
 * about you, so it belongs with the rest of what he knows, and it is the
 * first thing you see rather than something you go looking for.
 *
 * The detail is a sheet over the list, not a third column. You never lose
 * your place and back is one tap.
 */

/** The kinds, in the order the count line reads them. */
const VIEWS = ["about", "facts", "lessons", "wrong", "seen", "connections"] as const;
type View = (typeof VIEWS)[number];

const VIEW_LABEL: Record<View, string> = {
  about: "about you",
  facts: "facts",
  lessons: "lessons",
  wrong: "wrong guesses",
  seen: "seen",
  connections: "connections",
};

export default function MemoryPage() {
  const [counts, setCounts] = useState<MemoryCounts | null>(null);
  const [items, setItems] = useState<ListItem[]>([]);
  const [selected, setSelected] = useState<ListItem | null>(null);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [view, setView] = useTabParam<View>("view", "about", VIEWS);
  const [reflections, setReflections] = useState<ReflectionDTO[]>([]);
  const [reflectionChains, setReflectionChains] = useState<ReflectionChainDTO[]>([]);
  const [predictions, setPredictions] = useState<PredictionDTO[]>([]);

  const load = useCallback(async (v: View) => {
    setLoading(true);
    const c = await fetchMemoryCounts();
    setCounts(c);
    setReflections([]);
    setReflectionChains([]);
    setPredictions([]);
    if (v === "connections") {
      setItems([]);
    } else if (v === "facts" || v === "about") {
      // "About you" shows what he learned most recently alongside the boss
      // profile, so the same fetch serves both.
      setItems((await fetchMemories({})) ?? []);
    } else if (v === "seen") {
      setItems((await fetchObservations()) ?? []);
    } else if (v === "lessons") {
      setItems([]);
      const [r, c2] = await Promise.all([fetchReflections(), fetchReflectionChains()]);
      setReflections(r ?? []);
      setReflectionChains(c2 ?? []);
    } else {
      setItems([]);
      setPredictions((await fetchPredictions()) ?? []);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void load(view);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useRealtime(
    ["mem_observations", "mem_memories", "mem_reflections", "mem_reflection_chains", "mem_predictions"],
    () => {
      // A realtime push mid-search would clobber the results you are reading.
      if (query.trim()) return;
      void load(view);
    },
  );

  // Debounced search. No submit button: a search you have to confirm is a
  // search you stop using.
  useEffect(() => {
    const q = query.trim();
    if (!q) {
      void load(view);
      return;
    }
    const ac = new AbortController();
    const t = window.setTimeout(async () => {
      const res = await searchMemory(q, ac.signal);
      setItems(res ?? []);
      setLoading(false);
    }, 200);
    setLoading(true);
    return () => {
      window.clearTimeout(t);
      ac.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  const pick = (v: View) => {
    setView(v);
    setQuery("");
    void load(v);
  };

  const countItems = useMemo(
    () => [
      { value: counts?.memories ?? "-", label: VIEW_LABEL.facts, selected: view === "facts", onSelect: () => pick("facts") },
      // Lessons and wrong guesses are fetched per view, so their count is
      // only real while that view is open. Showing a stale "0" from another
      // view would read as "he has learned nothing", which is a lie — so the
      // number is omitted until it is actually known.
      {
        value: view === "lessons" ? reflections.length + reflectionChains.length : "",
        label: VIEW_LABEL.lessons,
        selected: view === "lessons",
        onSelect: () => pick("lessons"),
      },
      {
        value: view === "wrong" ? predictions.length : "",
        label: VIEW_LABEL.wrong,
        selected: view === "wrong",
        onSelect: () => pick("wrong"),
      },
      { value: counts?.observations ?? "-", label: VIEW_LABEL.seen, selected: view === "seen", onSelect: () => pick("seen") },
      { value: counts?.graph_nodes ?? "-", label: VIEW_LABEL.connections, selected: view === "connections", onSelect: () => pick("connections") },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [counts, view, reflections.length, reflectionChains.length, predictions.length],
  );

  const searching = query.trim().length > 0;

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto scroll-touch">
        <div className="mx-auto flex w-full min-w-0 max-w-list flex-col gap-4 px-4 py-5 sm:px-6">
          <h1 className="font-voice text-[26px] font-medium tracking-tight">Memory</h1>

          <SearchPage
            query={query}
            onQueryChange={setQuery}
            counts={
              <CountLine
                items={[
                  { value: "", label: "about you", selected: view === "about", onSelect: () => pick("about") },
                  ...countItems,
                ]}
              />
            }
          >
            <div className="flex min-w-0 flex-col">
              {searching ? (
                <SearchResults items={items} loading={loading} query={query} onPick={setSelected} selected={selected} />
              ) : view === "about" ? (
                <>
                  <BossProfilePanel />
                  {/* Compass, moved here from Settings. Your mission is a
                      fact about you, so it sits with the rest of what he
                      knows rather than in a preferences tab you had to
                      remember existed. Same editor, new home. */}
                  <GroupLabel label="What you are aiming at" />
                  <CompassSection />
                  <GroupLabel label="Learned today" />
                  <List items={items.slice(0, 8)} loading={loading} selected={selected} onPick={setSelected} empty="facts" />
                </>
              ) : view === "connections" ? (
                <div className="min-h-[420px]">
                  <KnowledgeGraphPanel />
                </div>
              ) : view === "lessons" ? (
                reflections.length + reflectionChains.length === 0 ? (
                  <EmptyState
                    icon={Sparkles}
                    title={loading ? "Loading…" : "Nothing learned yet"}
                    description="A lesson appears when he reflects on a finished session and draws a conclusion he can reuse."
                  />
                ) : (
                  <div className="flex flex-col gap-2 pt-2">
                    {reflectionChains.map((it) => (
                      <ReflectionChainRow key={it.id} item={it} />
                    ))}
                    {reflections.map((it) => (
                      <ReflectionRow key={it.id} item={it} />
                    ))}
                  </div>
                )
              ) : view === "wrong" ? (
                predictions.length === 0 ? (
                  <EmptyState
                    icon={Zap}
                    title={loading ? "Loading…" : "He has not been surprised lately"}
                    description="A wrong guess is logged when a tool result differs sharply from what he expected. It is how he notices his own blind spots."
                  />
                ) : (
                  <div className="flex flex-col gap-2 pt-2">
                    {predictions.map((it) => (
                      <PredictionRow key={it.id} item={it} />
                    ))}
                  </div>
                )
              ) : (
                <List
                  items={items}
                  loading={loading}
                  selected={selected}
                  onPick={setSelected}
                  empty={view === "seen" ? "seen" : "facts"}
                />
              )}
            </div>
          </SearchPage>
        </div>
      </div>

      <ResponsiveModal
        open={!!selected}
        onOpenChange={(o) => !o && setSelected(null)}
        title="Where this came from"
        size="lg"
      >
        <MemoryDetail source={selected} onClose={() => setSelected(null)} />
      </ResponsiveModal>
    </AppShell>
  );
}

function List({
  items,
  loading,
  selected,
  onPick,
  empty,
}: {
  items: ListItem[];
  loading: boolean;
  selected: ListItem | null;
  onPick: (i: ListItem) => void;
  empty: "facts" | "seen";
}) {
  if (items.length === 0) {
    if (loading) return <p className="py-3 text-sm text-muted-foreground">Loading…</p>;
    return empty === "facts" ? (
      <EmptyState
        icon={Brain}
        title="Nothing learned yet"
        description="Facts form as he compresses what he has seen about you. Keep talking to him in Chat and they start landing here."
      />
    ) : (
      <EmptyState
        icon={Eye}
        title="Nothing observed yet"
        description="Every message, tool call and decision in Chat is captured here first. Start a conversation to seed the stream."
      />
    );
  }
  return (
    <div className="flex flex-col gap-2 pt-2">
      {items.map((it, i) => (
        <MemoryCard
          key={selectedId(it) + ":" + i}
          source={it}
          active={selectedId(selected) === selectedId(it)}
          onClick={() => onPick(it)}
        />
      ))}
    </div>
  );
}

function SearchResults({
  items,
  loading,
  query,
  selected,
  onPick,
}: {
  items: ListItem[];
  loading: boolean;
  query: string;
  selected: ListItem | null;
  onPick: (i: ListItem) => void;
}) {
  if (loading) return <p className="py-3 text-sm text-muted-foreground">Looking…</p>;
  if (items.length === 0) {
    return (
      <EmptyState
        icon={Brain}
        title="Nothing matches that"
        description={`He has nothing filed under “${query.trim()}”. Try a broader phrase, or a person's name.`}
      />
    );
  }
  return (
    <>
      <GroupLabel label="Results" count={items.length} />
      <List items={items} loading={false} selected={selected} onPick={onPick} empty="facts" />
    </>
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
      <p className="mt-2 line-clamp-4 break-words text-sm">{item.critique || "-"}</p>
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

function ReflectionChainRow({ item }: { item: ReflectionChainDTO }) {
  return (
    <article className="rounded-xl border bg-card px-3 py-3">
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span className="inline-flex items-center gap-1 font-mono uppercase text-tier-procedural">
          <Sparkles className="size-3" aria-hidden />
          chain · {item.occurrences} sessions
        </span>
        <time dateTime={item.updated_at} suppressHydrationWarning>
          {new Date(item.updated_at).toLocaleString()}
        </time>
      </div>
      <p className="mt-2 line-clamp-4 break-words text-sm">{item.lesson || "-"}</p>
      <div className="mt-2 flex flex-wrap gap-1 text-[10px]">
        <span className="rounded-full bg-tier-procedural/10 px-2 py-0.5 font-mono text-tier-procedural">
          confidence {(item.confidence * 100).toFixed(0)}%
        </span>
        <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-muted-foreground">
          {item.topic}
        </span>
      </div>
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
        Expected: <span className="text-foreground">{item.expected || "-"}</span>
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
