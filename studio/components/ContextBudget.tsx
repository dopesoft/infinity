"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { fetchCoreStatus } from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";

const MODEL_LIMITS: Record<string, number> = {
  "claude-sonnet-4-5": 200_000,
  "claude-opus-4-5": 200_000,
  "claude-haiku-4-5": 200_000,
  "claude-3-5-sonnet": 200_000,
  "gpt-4o": 128_000,
  "gpt-4-turbo": 128_000,
  "gemini-1.5-pro": 1_000_000,
  "gemini-2.0-flash": 1_000_000,
};

function limitFor(model: string | undefined): number {
  if (!model) return 200_000;
  for (const k of Object.keys(MODEL_LIMITS)) {
    if (model.toLowerCase().startsWith(k)) return MODEL_LIMITS[k];
  }
  return 200_000;
}

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

export function ContextBudget({ usedTokens }: { usedTokens: number }) {
  const [model, setModel] = useState<string | undefined>(undefined);

  useEffect(() => {
    const ctl = new AbortController();
    fetchCoreStatus(ctl.signal).then((s) => setModel(s?.model));
    return () => ctl.abort();
  }, []);

  const max = limitFor(model);
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
