"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { fetchHeartbeats, type HeartbeatRunDTO } from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";

function relTime(iso?: string | null): string {
  if (!iso) return "-";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "-";
  const s = Math.max(1, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

const FINDING_DOT: Record<string, string> = {
  outcome: "bg-warning",
  pattern: "bg-info",
  surprise: "bg-success",
  failing_skill: "bg-destructive",
};

function summaryLines(summary: string): string[] {
  if (!summary) return [];
  return summary
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean)
    .slice(0, 3);
}

function dotColor(line: string): string {
  const lower = line.toLowerCase();
  if (lower.includes("outcome") || lower.includes("overdue")) return FINDING_DOT.outcome;
  if (lower.includes("pattern")) return FINDING_DOT.pattern;
  if (lower.includes("surprise") || lower.includes("prep")) return FINDING_DOT.surprise;
  if (lower.includes("fail") || lower.includes("error")) return FINDING_DOT.failing_skill;
  return "bg-muted-foreground";
}

export function LastHeartbeatPanel() {
  const [latest, setLatest] = useState<HeartbeatRunDTO | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const ctl = new AbortController();
    async function load() {
      const data = await fetchHeartbeats(ctl.signal);
      if (cancelled) return;
      setLatest(data?.runs?.[0] ?? null);
      setLoaded(true);
    }
    load();
    const id = setInterval(load, 30_000);
    return () => {
      cancelled = true;
      ctl.abort();
      clearInterval(id);
    };
  }, []);

  return (
    <SidePanelCard label="Last heartbeat">
      {!loaded ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : !latest ? (
        <p className="text-xs text-muted-foreground">No heartbeat runs yet.</p>
      ) : (
        <>
          <p className="text-[11px] text-muted-foreground" suppressHydrationWarning>
            {relTime(latest.started_at)} · {latest.findings} finding
            {latest.findings === 1 ? "" : "s"}
          </p>
          <ul className="mt-2 space-y-1.5">
            {summaryLines(latest.summary).map((line, i) => (
              <li key={i} className="flex items-start gap-2 text-[13px] leading-snug">
                <span
                  className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", dotColor(line))}
                />
                <span className="text-foreground/90">{line}</span>
              </li>
            ))}
          </ul>
          <Button asChild variant="ghost" size="sm" className="mt-2 h-8 w-full justify-between">
            <Link href="/heartbeat">
              View all
              <ArrowRight className="size-3.5" aria-hidden />
            </Link>
          </Button>
        </>
      )}
    </SidePanelCard>
  );
}
