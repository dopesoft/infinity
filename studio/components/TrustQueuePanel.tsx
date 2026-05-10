"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { fetchTrustContracts, type TrustContractDTO } from "@/lib/api";
import { SidePanelCard } from "@/components/SidePanelCard";

const RISK_DOT: Record<TrustContractDTO["risk_level"], string> = {
  low: "bg-success",
  medium: "bg-info",
  high: "bg-warning",
  critical: "bg-destructive",
};

export function TrustQueuePanel() {
  const [items, setItems] = useState<TrustContractDTO[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    const ctl = new AbortController();
    async function load() {
      const rows = await fetchTrustContracts("pending", ctl.signal);
      if (!cancelled) setItems(rows ?? []);
    }
    load();
    const id = setInterval(load, 15_000);
    return () => {
      cancelled = true;
      ctl.abort();
      clearInterval(id);
    };
  }, []);

  const count = items?.length ?? 0;

  return (
    <SidePanelCard
      label="Trust queue"
      action={
        count > 0 ? (
          <span className="inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-warning/20 px-1.5 font-mono text-[10px] font-semibold text-warning">
            {count}
          </span>
        ) : null
      }
    >
      {items === null ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : items.length === 0 ? (
        <p className="text-xs text-muted-foreground">No pending approvals.</p>
      ) : (
        <ul className="space-y-2">
          {items.slice(0, 3).map((c) => (
            <li key={c.id} className="space-y-0.5">
              <div className="flex items-center gap-2">
                <span
                  className={cn("size-1.5 shrink-0 rounded-full", RISK_DOT[c.risk_level])}
                />
                <span className="truncate text-[13px] font-medium">{c.title}</span>
              </div>
              {c.preview ? (
                <p className="line-clamp-1 pl-3.5 text-[11px] text-muted-foreground">
                  {c.preview}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {count > 3 ? (
        <Button asChild variant="ghost" size="sm" className="mt-2 h-8 w-full justify-between">
          <Link href="/trust">
            Review all {count}
            <ArrowRight className="size-3.5" aria-hidden />
          </Link>
        </Button>
      ) : null}
    </SidePanelCard>
  );
}
