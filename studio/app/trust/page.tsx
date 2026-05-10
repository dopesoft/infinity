"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, RefreshCw, X, Clock } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { RiskBadge } from "@/components/RiskBadge";
import { cn } from "@/lib/utils";
import {
  decideTrust,
  fetchTrustContracts,
  type TrustContractDTO,
} from "@/lib/api";

const STATUS_FILTERS = ["pending", "approved", "denied", "snoozed", "all"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

export default function TrustPage() {
  const [contracts, setContracts] = useState<TrustContractDTO[]>([]);
  const [selected, setSelected] = useState<TrustContractDTO | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("pending");
  const [showDetail, setShowDetail] = useState(false);
  const [loading, setLoading] = useState(true);

  async function load() {
    setLoading(true);
    const r = await fetchTrustContracts(statusFilter);
    setContracts(r ?? []);
    setLoading(false);
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusFilter]);

  async function decide(id: string, decision: "approved" | "denied" | "snoozed") {
    await decideTrust(id, decision);
    setSelected(null);
    setShowDetail(false);
    await load();
  }

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const x of contracts) c[x.status] = (c[x.status] ?? 0) + 1;
    return c;
  }, [contracts]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Trust Contracts</h2>
            <Badge variant="secondary" className="font-mono">
              {contracts.length}
            </Badge>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={load}
              aria-label="Refresh"
              className="ml-auto"
              disabled={loading}
            >
              <RefreshCw className="size-4" />
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-1 text-xs">
            {STATUS_FILTERS.map((s) => (
              <button
                key={s}
                onClick={() => setStatusFilter(s)}
                className={cn(
                  "rounded-md border px-2 py-1 font-mono uppercase tracking-wide",
                  statusFilter === s
                    ? "border-info bg-info/10 text-info"
                    : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
                )}
              >
                {s} {counts[s] ? `(${counts[s]})` : ""}
              </button>
            ))}
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          <aside
            className={cn(
              "min-h-0 flex-1 flex-col overflow-y-auto border-b bg-background scroll-touch lg:w-80 lg:border-b-0 lg:border-r",
              showDetail ? "hidden lg:flex" : "flex",
            )}
          >
            <div className="flex flex-col gap-2 px-3 pb-4 pt-3">
              {contracts.length === 0 ? (
                <p className="px-1 text-sm text-muted-foreground">
                  {loading ? "Loading…" : "Queue is empty."}
                </p>
              ) : (
                contracts.map((c) => (
                  <button
                    key={c.id}
                    type="button"
                    onClick={() => {
                      setSelected(c);
                      setShowDetail(true);
                    }}
                    className={cn(
                      "w-full rounded-xl border bg-card px-3 py-2 text-left transition-colors hover:bg-accent",
                      selected?.id === c.id && "border-info ring-1 ring-info",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="flex items-center gap-1.5">
                        <Clock className="size-3" aria-hidden />
                        <time suppressHydrationWarning>
                          {new Date(c.created_at).toLocaleString()}
                        </time>
                      </span>
                      <RiskBadge level={c.risk_level} />
                    </div>
                    <p className="mt-1 line-clamp-2 break-words text-sm font-semibold">{c.title}</p>
                    <p className="mt-1 text-[11px] uppercase text-muted-foreground">{c.source}</p>
                  </button>
                ))
              )}
            </div>
          </aside>

          <section
            className={cn(
              "min-h-0 flex-1 flex-col overflow-y-auto bg-background scroll-touch",
              showDetail ? "flex" : "hidden lg:flex",
            )}
          >
            {showDetail && selected && (
              <button
                onClick={() => setShowDetail(false)}
                className="border-b px-4 py-2 text-left text-xs text-muted-foreground lg:hidden"
              >
                ← back to list
              </button>
            )}
            {!selected ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                Select a contract to review.
              </div>
            ) : (
              <div className="flex flex-col gap-3 p-4">
                <header className="space-y-2">
                  <div className="flex items-center gap-2">
                    <RiskBadge level={selected.risk_level} />
                    <Badge variant="outline" className="font-mono uppercase">{selected.source}</Badge>
                    <Badge variant="secondary" className="font-mono uppercase">{selected.status}</Badge>
                  </div>
                  <h3 className="text-base font-semibold">{selected.title}</h3>
                </header>
                <Section title="Reasoning">
                  <p className="whitespace-pre-wrap break-words text-sm">{selected.reasoning || "—"}</p>
                </Section>
                <Section title="Action spec">
                  <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs scroll-touch">
                    {JSON.stringify(selected.action_spec, null, 2)}
                  </pre>
                </Section>
                {selected.preview && (
                  <Section title="Preview / dry-run">
                    <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs scroll-touch">
                      {selected.preview}
                    </pre>
                  </Section>
                )}
                {selected.cited_memory_ids?.length > 0 && (
                  <Section title="Cited memories">
                    <ul className="space-y-1 text-xs">
                      {selected.cited_memory_ids.map((id) => (
                        <li key={id}>
                          <code className="font-mono">{id}</code>
                        </li>
                      ))}
                    </ul>
                  </Section>
                )}
                {selected.status === "pending" && (
                  <div className="sticky bottom-0 flex flex-wrap items-center gap-2 border-t bg-background pt-3">
                    <Button onClick={() => decide(selected.id, "approved")}>
                      <Check className="mr-1 size-4" /> Approve
                    </Button>
                    <Button variant="ghost" onClick={() => decide(selected.id, "snoozed")}>
                      Snooze
                    </Button>
                    <Button variant="ghost" onClick={() => decide(selected.id, "denied")}>
                      <X className="mr-1 size-4" /> Deny
                    </Button>
                  </div>
                )}
              </div>
            )}
          </section>
        </div>
      </div>
    </TabFrame>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      {children}
    </section>
  );
}
