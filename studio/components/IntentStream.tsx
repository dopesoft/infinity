"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import {
  fetchGaugeRecent,
  fetchIntentRecent,
  type GaugeReadDTO,
  type IntentRecordDTO,
} from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";

const GAUGE_STYLES: Record<GaugeReadDTO["tier"], { label: string; cls: string }> = {
  glance: { label: "GLANCE", cls: "bg-muted text-muted-foreground" },
  standard: { label: "STANDARD", cls: "bg-info/15 text-info" },
  deep: { label: "DEEP", cls: "bg-primary/15 text-primary" },
};

const TOKEN_STYLES: Record<IntentRecordDTO["token"], { label: string; cls: string }> = {
  fast_intervention: {
    label: "FAST",
    cls: "bg-info/15 text-info",
  },
  silent: {
    label: "SILENT",
    cls: "bg-muted text-muted-foreground",
  },
  full_assistance: {
    label: "FULL",
    cls: "bg-primary/15 text-primary",
  },
};

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const diffMs = Date.now() - t;
  const s = Math.max(1, Math.floor(diffMs / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function IntentStream() {
  const [items, setItems] = useState<IntentRecordDTO[] | null>(null);
  const [gauge, setGauge] = useState<GaugeReadDTO | null>(null);

  useEffect(() => {
    let cancelled = false;
    const ctl = new AbortController();
    async function load() {
      const [rows, gauges] = await Promise.all([
        fetchIntentRecent(5, ctl.signal),
        fetchGaugeRecent(1, ctl.signal),
      ]);
      if (!cancelled) {
        setItems(rows ?? []);
        setGauge(gauges?.[0] ?? null);
      }
    }
    load();
    const id = setInterval(load, 8000);
    return () => {
      cancelled = true;
      ctl.abort();
      clearInterval(id);
    };
  }, []);

  const gaugeStyle = gauge ? GAUGE_STYLES[gauge.tier] ?? GAUGE_STYLES.standard : null;

  return (
    <SidePanelCard label="Intent stream">
      {gauge && gaugeStyle ? (
        <div className="mb-2 flex items-center gap-2 border-b border-border/60 pb-2">
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Effort
          </span>
          <span
            className={cn(
              "inline-flex shrink-0 items-center rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold tracking-wider",
              gaugeStyle.cls,
            )}
          >
            {gaugeStyle.label}
          </span>
          {gauge.reason ? (
            <span className="truncate text-[11px] text-muted-foreground">{gauge.reason}</span>
          ) : null}
        </div>
      ) : null}
      {items === null ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : items.length === 0 ? (
        <p className="text-xs text-muted-foreground">No turns classified yet.</p>
      ) : (
        <ul className="divide-y divide-border/60">
          {items.slice(0, 5).map((r) => {
            const style = TOKEN_STYLES[r.token] ?? TOKEN_STYLES.silent;
            return (
              <li key={r.id} className="space-y-1 py-2 first:pt-0 last:pb-0">
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      "inline-flex shrink-0 items-center rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold tracking-wider",
                      style.cls,
                    )}
                  >
                    {style.label}
                  </span>
                  <span className="font-mono text-[11px] text-muted-foreground">
                    {r.confidence.toFixed(2)}
                  </span>
                  <span
                    className="ml-auto text-[11px] text-muted-foreground"
                    suppressHydrationWarning
                  >
                    {relTime(r.created_at)}
                  </span>
                </div>
                <p className="line-clamp-2 text-[13px] text-foreground/90">
                  {r.reason || r.user_msg || "-"}
                </p>
              </li>
            );
          })}
        </ul>
      )}
    </SidePanelCard>
  );
}
