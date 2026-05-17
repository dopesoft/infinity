"use client";

import { ChevronRight, Clock, Link as LinkIcon, Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { TierBadge } from "@/components/TierBadge";
import type { SearchResult, ObservationDTO, MemoryDTO } from "@/lib/api";

type CardItem = {
  kind: "memory" | "observation" | "search";
  id: string;
  title?: string;
  hookName?: string;
  text: string;
  createdAt: string;
  sessionId?: string;
  score?: number;
  streams?: string[];
  tier?: string;
  status?: string;
};

function fromAny(s: SearchResult | ObservationDTO | MemoryDTO): CardItem {
  if ("observation_id" in s) {
    return {
      kind: "search",
      id: s.observation_id,
      hookName: s.hook_name,
      text: s.raw_text,
      createdAt: s.created_at,
      sessionId: s.session_id,
      score: s.score,
      streams: s.streams,
    };
  }
  if ("hook_name" in s) {
    return {
      kind: "observation",
      id: s.id,
      hookName: s.hook_name,
      text: s.raw_text,
      createdAt: s.created_at,
      sessionId: s.session_id,
    };
  }
  return {
    kind: "memory",
    id: s.id,
    title: s.title,
    text: s.content,
    createdAt: s.created_at,
    tier: s.tier,
    status: s.status,
  };
}

export function MemoryCard({
  source,
  active,
  onClick,
}: {
  source: SearchResult | ObservationDTO | MemoryDTO;
  active?: boolean;
  onClick?: () => void;
}) {
  const item = fromAny(source);

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
        <div className="flex min-w-0 items-center gap-1.5">
          {item.tier && <TierBadge tier={item.tier} stale={item.status === "superseded"} />}
          {item.hookName && <code className="truncate font-mono">{item.hookName}</code>}
        </div>
        <div className="flex items-center gap-1">
          <Clock className="size-3" aria-hidden />
          <time dateTime={item.createdAt} suppressHydrationWarning>
            {new Date(item.createdAt).toLocaleString()}
          </time>
        </div>
      </div>
      {item.title && <p className="mt-1 line-clamp-1 font-semibold">{item.title}</p>}
      <p className="mt-1 line-clamp-2 break-words text-sm">{item.text || "-"}</p>
      <div className="mt-1 flex flex-wrap items-center gap-1 text-[10px]">
        {item.streams?.map((s) => (
          <span
            key={s}
            className="inline-flex items-center gap-0.5 rounded-full bg-info/15 px-1.5 py-0.5 font-mono uppercase text-info"
          >
            <Search className="size-2.5" aria-hidden />
            {s}
          </span>
        ))}
        {typeof item.score === "number" && (
          <span className="font-mono text-muted-foreground">{item.score.toFixed(3)}</span>
        )}
        {item.sessionId && (
          <span className="ml-auto inline-flex items-center gap-0.5 truncate font-mono text-muted-foreground">
            <LinkIcon className="size-2.5" aria-hidden />
            {item.sessionId.slice(0, 8)}
          </span>
        )}
        <ChevronRight className="size-3 text-muted-foreground" aria-hidden />
      </div>
    </button>
  );
}
