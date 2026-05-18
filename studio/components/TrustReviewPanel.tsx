"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, Clock, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
} from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import {
  decideTrust,
  decideTrustBatch,
  fetchTrustContracts,
  type TrustContractDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

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

type Tab = "pending" | "decided";

// "Decided" rolls up everything the boss has already actioned: approvals
// (still actionable / consumed by Core) and denials. Snoozed is omitted
// because those rows are time-bombs that bounce back to pending and would
// otherwise double-count.
const DECIDED_STATUSES = ["approved", "consumed", "denied"] as const;

/**
 * TrustReviewPanel is the full Trust queue UI for /settings?section=trust.
 * It groups pending contracts by batch_id so a skill that queued multiple
 * actions in one run (inbox triage drafts, calendar prep replies, etc.)
 * surfaces as a single "Approve all N" block. Contracts without a
 * batch_id keep the original per-row flow.
 *
 * The Decided tab is a read-only audit log of recent approvals/denials,
 * sorted newest-first by decided_at — same source endpoint, just a
 * different status filter. This is what fulfills the "audit what you've
 * already trusted" half of the section description.
 *
 * The lightweight TrustQueuePanel is unchanged - it stays the
 * dashboard side-card; this is the surface the boss bulk-clears from.
 */
export function TrustReviewPanel() {
  const [tab, setTab] = useState<Tab>("pending");
  const [pending, setPending] = useState<TrustContractDTO[] | null>(null);
  const [decided, setDecided] = useState<TrustContractDTO[] | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});

  const loadPending = useCallback(async (signal?: AbortSignal) => {
    const rows = await fetchTrustContracts("pending", signal);
    setPending(rows ?? []);
  }, []);

  const loadDecided = useCallback(async (signal?: AbortSignal) => {
    // Fan out one fetch per status, merge, sort by decided_at desc.
    // Server caps each status at 50 rows; we cap the merged view at 100
    // so the page doesn't grow unbounded as the audit log fills up.
    const results = await Promise.all(
      DECIDED_STATUSES.map((s) => fetchTrustContracts(s, signal)),
    );
    const merged = results.flatMap((r) => r ?? []);
    merged.sort((a, b) => {
      const at = a.decided_at ?? a.created_at;
      const bt = b.decided_at ?? b.created_at;
      return (bt ?? "").localeCompare(at ?? "");
    });
    setDecided(merged.slice(0, 100));
  }, []);

  useEffect(() => {
    const ctl = new AbortController();
    loadPending(ctl.signal);
    const id = setInterval(() => loadPending(), 15_000);
    return () => {
      ctl.abort();
      clearInterval(id);
    };
  }, [loadPending]);

  // Decided list loads lazily on first tab switch, then refreshes when the
  // boss returns to the tab. Polling isn't needed - it changes only when
  // the boss themselves clicks Approve/Deny.
  useEffect(() => {
    if (tab !== "decided") return;
    const ctl = new AbortController();
    loadDecided(ctl.signal);
    return () => ctl.abort();
  }, [tab, loadDecided]);

  // Push updates the moment a new contract lands so the boss never sees
  // "No pending approvals" during the 15s polling gap after landing here
  // from the TrustToast. Same channel the toast subscribes to. Decided
  // list also refreshes since approvals flip status on the same table.
  useRealtime("mem_trust_contracts", () => {
    void loadPending();
    if (tab === "decided") void loadDecided();
  });

  const pendingGroups = useMemo<Group[]>(() => {
    if (!pending) return [];
    const map = new Map<string, TrustContractDTO[]>();
    const ungrouped: TrustContractDTO[] = [];
    for (const c of pending) {
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
  }, [pending]);

  const decideOne = useCallback(
    async (id: string, decision: "approved" | "denied") => {
      setBusy((b) => ({ ...b, [id]: true }));
      const ok = await decideTrust(id, decision);
      setBusy((b) => {
        const next = { ...b };
        delete next[id];
        return next;
      });
      if (ok) {
        await loadPending();
        if (tab === "decided") await loadDecided();
      }
    },
    [loadPending, loadDecided, tab],
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
      await loadPending();
      if (tab === "decided") await loadDecided();
    },
    [loadPending, loadDecided, tab],
  );

  const pendingCount = pending?.length ?? 0;
  const decidedCount = decided?.length ?? 0;

  return (
    <div className="space-y-3">
      <TabBar
        tab={tab}
        onChange={setTab}
        pendingCount={pendingCount}
        decidedCount={decidedCount}
      />

      {tab === "pending" ? (
        pending === null ? (
          <p className="text-xs text-muted-foreground">Loading…</p>
        ) : pending.length === 0 ? (
          <p className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
            No pending approvals. New requests will appear here.
          </p>
        ) : (
          <div className="space-y-3">
            {pendingGroups.map((g) =>
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
        )
      ) : decided === null ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : decided.length === 0 ? (
        <p className="rounded-md border border-border bg-card p-3 text-xs text-muted-foreground">
          No decisions yet. Approvals and denials show up here.
        </p>
      ) : (
        <ul className="space-y-2">
          {decided.map((c) => (
            <DecidedRow key={c.id} contract={c} />
          ))}
        </ul>
      )}
    </div>
  );
}

function TabBar({
  tab,
  onChange,
  pendingCount,
  decidedCount,
}: {
  tab: Tab;
  onChange: (next: Tab) => void;
  pendingCount: number;
  decidedCount: number;
}) {
  return (
    <PageTabs value={tab} onValueChange={(v) => onChange(v as Tab)}>
      <PageTabsList scrollable>
        <PageTabsTrigger value="pending">
          <span>Pending</span>
          {pendingCount > 0 ? <CountBadge active={tab === "pending"} count={pendingCount} /> : null}
        </PageTabsTrigger>
        <PageTabsTrigger value="decided">
          <span>Decided</span>
          {decidedCount > 0 ? <CountBadge active={tab === "decided"} count={decidedCount} /> : null}
        </PageTabsTrigger>
      </PageTabsList>
    </PageTabs>
  );
}

function CountBadge({ active, count }: { active: boolean; count: number }) {
  return (
    <span
      className={cn(
        "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
        active ? "bg-background text-foreground" : "bg-muted-foreground/15 text-muted-foreground",
      )}
      aria-label={`${count} ${count === 1 ? "item" : "items"}`}
    >
      {count}
    </span>
  );
}

function DecidedRow({ contract }: { contract: TrustContractDTO }) {
  const decidedAt = contract.decided_at ?? contract.created_at;
  const isApproved =
    contract.status === "approved" || contract.status === "consumed";
  return (
    <li className="rounded-md border border-border bg-card p-3">
      <div className="flex flex-col gap-1.5 sm:flex-row sm:items-start sm:justify-between">
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
            {contract.source} ·{" "}
            <Clock className="-mt-0.5 mr-0.5 inline size-2.5" aria-hidden />
            {formatRelative(decidedAt)}
            {contract.decision_note ? ` · "${contract.decision_note}"` : ""}
          </p>
        </div>
        <DecisionBadge
          status={contract.status}
          tone={isApproved ? "approved" : "denied"}
        />
      </div>
    </li>
  );
}

function DecisionBadge({
  status,
  tone,
}: {
  status: TrustContractDTO["status"];
  tone: "approved" | "denied";
}) {
  const label =
    status === "consumed"
      ? "Approved · consumed"
      : status.charAt(0).toUpperCase() + status.slice(1);
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 self-start rounded-full border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wide",
        tone === "approved"
          ? "border-success/30 bg-success/10 text-success"
          : "border-destructive/30 bg-destructive/10 text-destructive",
      )}
    >
      {tone === "approved" ? (
        <Check className="size-3" aria-hidden />
      ) : (
        <X className="size-3" aria-hidden />
      )}
      {label}
    </span>
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
