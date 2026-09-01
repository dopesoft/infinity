"use client";

import { useCallback, useEffect, useState } from "react";
import { ShieldAlert, ChevronDown, ChevronUp, Check, X } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { fetchTrustContracts, decideTrust, type TrustContractDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { cn } from "@/lib/utils";

/**
 * PendingApprovalsDock - always-visible "approvals waiting" strip pinned above
 * the composer, mirroring BackgroundJobDock.
 *
 * Inline approval already exists on the LIVE turn's gated tool (ToolCallCard),
 * but a contract from background work (cron / heartbeat / self-improve), or one
 * that scrolled off or survived a reload, has no card - so the boss was forced
 * to leave chat for the /trust page. This dock is the catch: it reads ALL
 * pending mem_trust_contracts (server state + realtime, so it survives
 * navigation / refresh / second device) and lets the boss approve or deny right
 * here. No tab-switch, ever.
 *
 * Reads the same /api/trust-contracts API and decideTrust call the Trust page
 * uses, so the two stay perfectly in sync.
 */
export function PendingApprovalsDock() {
  const [rows, setRows] = useState<TrustContractDTO[]>([]);
  const [expanded, setExpanded] = useState(true);
  const [deciding, setDeciding] = useState<Record<string, "approved" | "denied">>({});

  const load = useCallback(async (signal?: AbortSignal) => {
    const res = await fetchTrustContracts("pending", signal);
    if (Array.isArray(res)) setRows(res);
  }, []);

  useEffect(() => {
    const ctl = new AbortController();
    void load(ctl.signal);
    // Belt-and-braces poll in case a realtime tick is dropped on reconnect.
    const id = setInterval(() => void load(), 15_000);
    return () => {
      ctl.abort();
      clearInterval(id);
    };
  }, [load]);

  useRealtime("mem_trust_contracts", () => void load());

  const decide = useCallback(
    async (id: string, decision: "approved" | "denied") => {
      if (deciding[id]) return;
      setDeciding((d) => ({ ...d, [id]: decision }));
      const ok = await decideTrust(id, decision);
      if (ok) {
        // Drop it optimistically; realtime/poll will confirm.
        setRows((r) => r.filter((c) => c.id !== id));
      }
      setDeciding((d) => {
        const next = { ...d };
        delete next[id];
        return next;
      });
    },
    [deciding],
  );

  if (rows.length === 0) return null;

  return (
    <div className="min-w-0 shrink-0 border-t border-warning/40 bg-warning/[0.08] px-3 py-2 sm:px-4">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full min-w-0 items-center gap-2.5"
      >
        <ShieldAlert className="size-4 shrink-0 text-warning" aria-hidden />
        <span className="min-w-0 flex-1 truncate text-left text-xs font-semibold text-warning">
          {rows.length} approval{rows.length === 1 ? "" : "s"} waiting
        </span>
        {expanded ? (
          <ChevronDown className="size-4 shrink-0 text-warning/70" aria-hidden />
        ) : (
          <ChevronUp className="size-4 shrink-0 text-warning/70" aria-hidden />
        )}
      </button>

      {expanded ? (
        <ul className="mt-2 space-y-2 border-t border-warning/25 pt-2">
          {rows.map((c) => (
            <li key={c.id} className="min-w-0 space-y-1.5">
              <div className="flex min-w-0 items-start gap-2">
                <RiskDot level={c.risk_level} />
                <div className="min-w-0 flex-1">
                  <p className="min-w-0 break-words text-[12px] font-medium leading-snug text-foreground [overflow-wrap:anywhere]">
                    {c.title || c.source}
                  </p>
                  {(c.reasoning || c.preview)?.trim() ? (
                    <p className="mt-0.5 line-clamp-2 break-words text-[11px] leading-snug text-muted-foreground [overflow-wrap:anywhere]">
                      {(c.reasoning || c.preview).trim()}
                    </p>
                  ) : null}
                </div>
              </div>
              <div className="flex items-center gap-2 pl-[18px]">
                <button
                  type="button"
                  onClick={() => void decide(c.id, "approved")}
                  disabled={!!deciding[c.id]}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md bg-success px-3 text-[12px] font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-60"
                >
                  {deciding[c.id] === "approved" ? (
                    <Spinner className="size-3.5" aria-hidden />
                  ) : (
                    <Check className="size-3.5" aria-hidden />
                  )}
                  Approve
                </button>
                <button
                  type="button"
                  onClick={() => void decide(c.id, "denied")}
                  disabled={!!deciding[c.id]}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[12px] font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-60"
                >
                  {deciding[c.id] === "denied" ? (
                    <Spinner className="size-3.5" aria-hidden />
                  ) : (
                    <X className="size-3.5" aria-hidden />
                  )}
                  Deny
                </button>
                {c.source ? (
                  <span className="ml-auto shrink-0 truncate font-mono text-[10px] text-muted-foreground">
                    {c.source.replace(/_/g, " ")}
                  </span>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function RiskDot({ level }: { level: TrustContractDTO["risk_level"] }) {
  const tone =
    level === "critical" || level === "high"
      ? "bg-danger"
      : level === "medium"
        ? "bg-warning"
        : "bg-muted-foreground/50";
  return <span className={cn("mt-1 size-2 shrink-0 rounded-full", tone)} aria-label={`${level} risk`} />;
}
