"use client";

import { IconChevronRight, IconClock, IconLink, IconSearch } from "@tabler/icons-react";
import { cn } from "@/lib/utils";
import type { SearchResult, ObservationDTO } from "@/lib/api";

type Item = {
  id: string;
  hookName: string;
  text: string;
  createdAt: string;
  sessionId?: string;
  score?: number;
  streams?: string[];
};

function fromSearchResult(r: SearchResult): Item {
  return {
    id: r.observation_id,
    hookName: r.hook_name,
    text: r.raw_text,
    createdAt: r.created_at,
    sessionId: r.session_id,
    score: r.score,
    streams: r.streams,
  };
}

function fromObservation(o: ObservationDTO): Item {
  return {
    id: o.id,
    hookName: o.hook_name,
    text: o.raw_text,
    createdAt: o.created_at,
    sessionId: o.session_id,
  };
}

export function MemoryCard({
  source,
  active,
  onClick,
}: {
  source: SearchResult | ObservationDTO;
  active?: boolean;
  onClick?: () => void;
}) {
  const item: Item =
    "observation_id" in source ? fromSearchResult(source) : fromObservation(source);

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full rounded-xl border bg-card px-3 py-2 text-left transition-colors",
        "hover:bg-accent",
        active && "border-info ring-1 ring-info",
      )}
    >
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <code className="font-mono">{item.hookName}</code>
        <div className="flex items-center gap-1">
          <IconClock className="size-3" aria-hidden />
          <time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time>
        </div>
      </div>
      <p className="mt-1 line-clamp-2 break-words text-sm">{item.text || "—"}</p>
      <div className="mt-1 flex flex-wrap items-center gap-1 text-[10px]">
        {item.streams?.map((s) => (
          <span
            key={s}
            className="inline-flex items-center gap-0.5 rounded-full bg-info/15 px-1.5 py-0.5 font-mono uppercase text-info"
          >
            <IconSearch className="size-2.5" aria-hidden />
            {s}
          </span>
        ))}
        {typeof item.score === "number" && (
          <span className="font-mono text-muted-foreground">{item.score.toFixed(3)}</span>
        )}
        {item.sessionId && (
          <span className="ml-auto inline-flex items-center gap-0.5 truncate font-mono text-muted-foreground">
            <IconLink className="size-2.5" aria-hidden />
            {item.sessionId.slice(0, 8)}
          </span>
        )}
        <IconChevronRight className="size-3 text-muted-foreground" aria-hidden />
      </div>
    </button>
  );
}
