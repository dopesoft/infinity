"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  decideTrust,
  decideTrustBatch,
  fetchTrustContracts,
  type TrustContractDTO,
} from "@/lib/api";

const RISK_DOT: Record<TrustContractDTO["risk_level"], string> = {
  low: "bg-success",
  medium: "bg-info",
  high: "bg-warning",
  critical: "bg-destructive",
};

type Group = {
  batchId: string | null;
  items: TrustContractDTO[];
};

/**
 * TrustReviewPanel is the full Trust queue UI for /settings?section=trust.
 * It groups pending contracts by batch_id so a skill that queued multiple
 * actions in one run (inbox triage drafts, calendar prep replies, etc.)
 * surfaces as a single "Approve all N" block. Contracts without a
 * batch_id keep the original per-row flow.
 *
 * The lightweight TrustQueuePanel is unchanged - it stays the
 * dashboard side-card; this is the surface the boss bulk-clears from.
 */
export function TrustReviewPanel() {
  const [items, setItems] = useState<TrustContractDTO[] | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});

  const load = useCallback(async (signal?: AbortSignal) => {
    const rows = await fetchTrustContracts("pending", signal);
    setItems(rows ?? []);
  }, []);

  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal);
    const id = setInterval(() => load(), 15_000);
    return () => {
      ctl.abort();
      clearInterval(id);
    };
  }, [load]);

  const groups = useMemo<Group[]>(() => {
    if (!items) return [];
    const map = new Map<string, TrustContractDTO[]>();
    const ungrouped: TrustContractDTO[] = [];
    for (const c of items) {
      if (c.batch_id) {
        const list = map.get(c.batch_id) ?? [];
        list.push(c);
        map.set(c.batch_id, list);
      } else {
        ungrouped.push(c);
      }
    }
    const batched: Group[] = Array.from(map.entries()).map(([batchId, list]) => ({
      batchId,
      items: list,
    }));
    // Most-recent batch first; ungrouped at the bottom for predictable scroll.
    batched.sort((a, b) =>
      (b.items[0]?.created_at ?? "").localeCompare(a.items[0]?.created_at ?? ""),
    );
    return [...batched, { batchId: null, items: ungrouped }];
  }, [items]);

  const decideOne = useCallback(
    async (id: string, decision: "approved" | "denied") => {
      setBusy((b) => ({ ...b, [id]: true }));
      const ok = await decideTrust(id, decision);
      setBusy((b) => {
        const next = { ...b };
        delete next[id];
        return next;
      });
      if (ok) await load();
    },
    [load],
  );

  const decideBatchAction = useCallback(
    async (batchId: string, decision: "approved" | "denied") => {
      const key = `batch:${batchId}`;
      setBusy((b) => ({ ...b, [key]: true }));
      await decideTrustBatch(batchId, decision);
      setBusy((b) => {
        const next = { ...b };
        delete next[key];
        return next;
      });
      await load();
    },
    [load],
  );

  if (items === null) {
    return <p className="text-xs text-muted-foreground">Loading…</p>;
  }
  if (items.length === 0) {
    return (
      <p className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
        No pending approvals. New requests will appear here.
      </p>
    );
  }

  return (
    <div className="space-y-3">
      {groups.map((g) =>
        g.items.length === 0 ? null : g.batchId ? (
          <BatchCard
            key={g.batchId}
            batchId={g.batchId}
            items={g.items}
            busy={!!busy[`batch:${g.batchId}`]}
            onDecide={decideBatchAction}
          />
        ) : (
          <ul key="ungrouped" className="space-y-2">
            {g.items.map((c) => (
              <SingleRow
                key={c.id}
                contract={c}
                busy={!!busy[c.id]}
                onDecide={decideOne}
              />
            ))}
          </ul>
        ),
      )}
    </div>
  );
}

function BatchCard({
  batchId,
  items,
  busy,
  onDecide,
}: {
  batchId: string;
  items: TrustContractDTO[];
  busy: boolean;
  onDecide: (batchId: string, decision: "approved" | "denied") => void;
}) {
  const first = items[0];
  const title = batchTitle(items);
  const highestRisk = highestRiskLevel(items);
  return (
    <section className="rounded-md border border-border bg-card p-3">
      <header className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 space-y-0.5">
          <div className="flex items-center gap-2">
            <span className={cn("size-1.5 shrink-0 rounded-full", RISK_DOT[highestRisk])} />
            <span className="truncate text-sm font-semibold">{title}</span>
            <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-mono uppercase tracking-wide text-muted-foreground">
              batch · {items.length}
            </span>
          </div>
          <p className="pl-3.5 text-[11px] text-muted-foreground">
            {first?.source ?? "skill"} · queued {formatRelative(first?.created_at)}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() => onDecide(batchId, "denied")}
          >
            <X className="mr-1 size-3.5" aria-hidden />
            Deny all
          </Button>
          <Button
            size="sm"
            disabled={busy}
            onClick={() => onDecide(batchId, "approved")}
          >
            <Check className="mr-1 size-3.5" aria-hidden />
            Approve all {items.length}
          </Button>
        </div>
      </header>
      <ul className="mt-2 space-y-1 border-t border-border pt-2">
        {items.slice(0, 6).map((c) => (
          <li key={c.id} className="space-y-0.5">
            <p className="truncate text-[12px]">{c.title}</p>
            {c.preview ? (
              <p className="line-clamp-1 text-[11px] text-muted-foreground">{c.preview}</p>
            ) : null}
          </li>
        ))}
        {items.length > 6 ? (
          <li className="text-[11px] text-muted-foreground">
            + {items.length - 6} more in this batch
          </li>
        ) : null}
      </ul>
    </section>
  );
}

function SingleRow({
  contract,
  busy,
  onDecide,
}: {
  contract: TrustContractDTO;
  busy: boolean;
  onDecide: (id: string, decision: "approved" | "denied") => void;
}) {
  return (
    <li className="rounded-md border border-border bg-card p-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 space-y-0.5">
          <div className="flex items-center gap-2">
            <span
              className={cn(
                "size-1.5 shrink-0 rounded-full",
                RISK_DOT[contract.risk_level],
              )}
            />
            <span className="truncate text-sm font-medium">{contract.title}</span>
          </div>
          {contract.preview ? (
            <p className="line-clamp-2 pl-3.5 text-[11px] text-muted-foreground">
              {contract.preview}
            </p>
          ) : null}
          <p className="pl-3.5 text-[10px] text-muted-foreground">
            {contract.source} · {formatRelative(contract.created_at)}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() => onDecide(contract.id, "denied")}
          >
            <X className="mr-1 size-3.5" aria-hidden />
            Deny
          </Button>
          <Button
            size="sm"
            disabled={busy}
            onClick={() => onDecide(contract.id, "approved")}
          >
            <Check className="mr-1 size-3.5" aria-hidden />
            Approve
          </Button>
        </div>
      </div>
    </li>
  );
}

function batchTitle(items: TrustContractDTO[]): string {
  if (items.length === 0) return "Batch";
  // Find a sensible common label - either the source-prefixed action verb
  // or the most common title across the batch. Cheap heuristic.
  const counts = new Map<string, number>();
  for (const c of items) {
    counts.set(c.title, (counts.get(c.title) ?? 0) + 1);
  }
  let topTitle = items[0].title;
  let topCount = 0;
  counts.forEach((n, t) => {
    if (n > topCount) {
      topCount = n;
      topTitle = t;
    }
  });
  return topCount === items.length ? topTitle : `${items.length} actions from ${items[0].source}`;
}

function highestRiskLevel(items: TrustContractDTO[]): TrustContractDTO["risk_level"] {
  const order: TrustContractDTO["risk_level"][] = [
    "critical",
    "high",
    "medium",
    "low",
  ];
  for (const lvl of order) {
    if (items.some((c) => c.risk_level === lvl)) return lvl;
  }
  return "medium";
}

function formatRelative(iso?: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const diffMs = Date.now() - t;
  if (diffMs < 60_000) return "just now";
  const min = Math.round(diffMs / 60_000);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.round(hr / 24);
  return `${d}d ago`;
}
