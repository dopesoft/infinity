"use client";

import { useEffect, useState } from "react";
import { IconPlus, IconArrowBackUp, IconArchive } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";

function shortId(id: string): string {
  if (!id) return "—";
  const tail = id.replace(/-/g, "").slice(-8);
  if (tail.length < 8) return tail;
  return `${tail.slice(0, 4)}-${tail.slice(4)}`;
}

function relStarted(ms: number, now: number): string {
  const diff = Math.max(0, now - ms);
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m ago`;
}

export function SessionHeader({
  sessionId,
  startedAt,
  onNew,
  onClear,
  onRewind,
}: {
  sessionId: string;
  startedAt?: number | null;
  onNew: () => void;
  onClear: () => void;
  onRewind?: () => void;
}) {
  const [now, setNow] = useState(0);
  useEffect(() => {
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="flex items-center justify-between gap-2 border-b px-3 py-2 sm:px-4">
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
          Session
        </span>
        <code
          className="truncate font-mono text-xs font-semibold text-foreground"
          suppressHydrationWarning
        >
          {shortId(sessionId)}
        </code>
        {startedAt && now ? (
          <span
            className="hidden text-[11px] text-muted-foreground sm:inline"
            suppressHydrationWarning
          >
            · started {relStarted(startedAt, now)}
          </span>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {onRewind ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={onRewind}
            aria-label="Rewind to a prior turn"
            title="Rewind (coming soon)"
            disabled
          >
            <IconArrowBackUp className="size-4" />
            <span className="hidden sm:inline">rewind</span>
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          onClick={onClear}
          aria-label="Compact session — fold into memory and clear visible context"
          title="Compact session"
        >
          <IconArchive className="size-4" />
          <span className="hidden sm:inline">/compact</span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={onNew}
          aria-label="Start a new session"
          title="New session"
        >
          <IconPlus className="size-4" />
          <span className="hidden sm:inline">/new</span>
        </Button>
      </div>
    </div>
  );
}
