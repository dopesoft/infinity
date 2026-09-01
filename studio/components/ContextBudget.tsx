"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { fetchContextUsage } from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";

// The window a model actually has is decided in ONE place, Core's
// /api/context/usage, which knows the vendor, the tier aliases the plan brain
// runs under and whether a standby is answering. A second table here drifted
// from it and told the boss his 1M window was 200K, so this reads the same
// number the composer's meter reads.

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

export function ContextBudget({ usedTokens }: { usedTokens: number }) {
  const [max, setMax] = useState<number | undefined>(undefined);

  useEffect(() => {
    const ctl = new AbortController();
    fetchContextUsage(undefined, ctl.signal).then((u) => {
      if (u && u.context_window > 0) setMax(u.context_window);
    });
    return () => ctl.abort();
  }, []);

  // Until Core answers, show the fill without inventing a denominator.
  if (!max) return null;
  const pct = Math.min(100, Math.round((usedTokens / max) * 100));
  const danger = pct >= 60;
  const critical = pct >= 80;

  return (
    <SidePanelCard label="Context">
      <div className="flex items-baseline gap-2">
        <span
          className={cn(
            "font-mono text-3xl font-semibold leading-none tabular-nums",
            critical ? "text-destructive" : danger ? "text-warning" : "text-foreground",
          )}
        >
          {pct}%
        </span>
        <span className="text-[11px] text-muted-foreground">
          of {formatTokens(max)} tokens
        </span>
      </div>
      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            "h-full rounded-full transition-all",
            critical ? "bg-destructive" : danger ? "bg-warning" : "bg-info",
          )}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-2 text-[11px] text-muted-foreground">
        {critical
          ? "Critical - compact recommended"
          : danger
            ? "Danger zone - compact soon"
            : "Danger zone at 60%"}
      </p>
    </SidePanelCard>
  );
}
