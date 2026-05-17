"use client";

import { useEffect, useState } from "react";
import {
  fetchCoreStatus,
  fetchMemoryCounts,
  fetchSkills,
  type CoreStatus,
  type MemoryCounts,
  type SkillSummaryDTO,
} from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";
import { formatUptime, getBootedAt } from "@/lib/uptime";

function compactNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

export function SystemPanel({ wsConnected }: { wsConnected: boolean }) {
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [counts, setCounts] = useState<MemoryCounts | null>(null);
  const [skills, setSkills] = useState<SkillSummaryDTO[] | null>(null);
  const [now, setNow] = useState(0);
  const [bootedAt, setBootedAt] = useState<number | null>(null);

  useEffect(() => {
    setBootedAt(getBootedAt());
    setNow(Date.now());
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, []);

  useEffect(() => {
    let cancelled = false;
    const ctl = new AbortController();
    async function load() {
      const [s, c, k] = await Promise.all([
        fetchCoreStatus(ctl.signal),
        fetchMemoryCounts(ctl.signal),
        fetchSkills(ctl.signal),
      ]);
      if (cancelled) return;
      setStatus(s);
      setCounts(c);
      setSkills(k);
    }
    load();
    const id = setInterval(load, 20_000);
    return () => {
      cancelled = true;
      ctl.abort();
      clearInterval(id);
    };
  }, []);

  const activeSkills = skills?.filter((s) => s.status === "active").length ?? 0;
  const provider = status?.provider ?? "-";
  const memoryStoreLabel = counts ? "healthy" : wsConnected ? "warming" : "offline";
  const memoryStoreClass = counts
    ? "text-success"
    : wsConnected
      ? "text-info"
      : "text-muted-foreground";

  return (
    <SidePanelCard label="System">
      <dl className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-1.5 text-[12px]">
        <dt className="text-muted-foreground">Memory store</dt>
        <dd className={memoryStoreClass}>{memoryStoreLabel}</dd>

        <dt className="text-muted-foreground">LLM provider</dt>
        <dd className="font-mono capitalize">{provider}</dd>

        <dt className="text-muted-foreground">Observations</dt>
        <dd className="font-mono tabular-nums">
          {counts ? compactNum(counts.observations) : "-"}
        </dd>

        <dt className="text-muted-foreground">Memories</dt>
        <dd className="font-mono tabular-nums">
          {counts ? compactNum(counts.memories) : "-"}
        </dd>

        <dt className="text-muted-foreground">Skills active</dt>
        <dd className="font-mono tabular-nums">{activeSkills}</dd>

        <dt className="text-muted-foreground">Uptime</dt>
        <dd className="font-mono tabular-nums" suppressHydrationWarning>
          {bootedAt && now ? formatUptime(now - bootedAt) : "-"}
        </dd>
      </dl>
    </SidePanelCard>
  );
}
