"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Activity, Brain, Clock, MessageSquare, Sparkles, type LucideIcon } from "lucide-react";
import { searchAll, type SearchHit } from "@/lib/api";

/**
 * useGlobalSearch — the one global search, for every box that runs one.
 *
 * There are two: the ⌘K palette and the dashboard's own field. They were about
 * to hold two copies of the same debounce, the same AbortController, the same
 * stale-response guard and the same kind→label map, which is how the two boxes
 * start answering the same question differently.
 *
 * The stale-response guard is not optional and is the reason this is a hook
 * rather than a fetch call: the input is keystroke-driven, so a slow response
 * for "in" must never land after a fast one for "inbox". Aborting is not
 * enough on its own — an abort races — so a monotonic sequence number decides
 * which response is allowed to write.
 *
 * `failed` is deliberately separate from an empty `hits`. `searchAll` returns
 * null on any transport or auth failure, and rendering that as "nothing
 * matches" would tell the boss his thing does not exist when the truth is that
 * we could not look. Consumers MUST render the two differently.
 */

export const KIND_META: Record<string, { label: string; Icon: LucideIcon; order: number }> = {
  surfaced: { label: "Needs you", Icon: Sparkles, order: 0 },
  memory: { label: "Memory", Icon: Brain, order: 1 },
  skill: { label: "Skills", Icon: Sparkles, order: 2 },
  automation: { label: "Automations", Icon: Clock, order: 3 },
  session: { label: "Conversations", Icon: MessageSquare, order: 4 },
  lesson: { label: "Lessons", Icon: Brain, order: 5 },
  prediction: { label: "Where he was wrong", Icon: Brain, order: 6 },
  observation: { label: "Everything seen", Icon: Brain, order: 7 },
};

export function kindMeta(kind: string) {
  return (
    KIND_META[kind] ?? {
      // An unknown kind is a table Core learned to search after this shipped.
      // It renders under its own name rather than being dropped — the whole
      // point of the generic contract is that a new source needs no client
      // change to become visible.
      label: kind.charAt(0).toUpperCase() + kind.slice(1),
      Icon: Activity,
      order: 50,
    }
  );
}

/** Keystroke debounce. Long enough to skip the middle of a word, short enough
 *  that results feel like they are keeping up. */
const DEBOUNCE_MS = 140;

export type GlobalSearch = {
  hits: SearchHit[];
  /** Hits grouped by kind, ordered by KIND_META.order. */
  groups: [string, SearchHit[]][];
  /** A request is in flight (including during the debounce window). */
  loading: boolean;
  /** The search could not run. NOT the same as zero results. */
  failed: boolean;
};

export function useGlobalSearch(query: string, enabled = true): GlobalSearch {
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const q = query.trim();
    if (!enabled || !q) {
      // Bump the sequence so an in-flight response for the query being
      // cleared cannot repopulate an emptied list.
      seq.current++;
      setHits([]);
      setLoading(false);
      setFailed(false);
      return;
    }
    const mine = ++seq.current;
    const ac = new AbortController();
    setLoading(true);
    const t = window.setTimeout(async () => {
      const res = await searchAll(q, ac.signal);
      if (mine !== seq.current) return;
      // An aborted request is not a failure — it is a request we chose to
      // discard, and the newer one owns the state.
      if (ac.signal.aborted) return;
      setHits(res?.hits ?? []);
      setFailed(res === null);
      setLoading(false);
    }, DEBOUNCE_MS);
    return () => {
      window.clearTimeout(t);
      ac.abort();
    };
  }, [query, enabled]);

  const groups = useMemo(() => {
    const by = new Map<string, SearchHit[]>();
    for (const hit of hits) {
      const list = by.get(hit.kind);
      if (list) list.push(hit);
      else by.set(hit.kind, [hit]);
    }
    return [...by.entries()].sort((a, b) => kindMeta(a[0]).order - kindMeta(b[0]).order);
  }, [hits]);

  return { hits, groups, loading, failed };
}
