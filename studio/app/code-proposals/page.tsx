"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, X, FileCode, RefreshCw, Search, Sparkles, Zap, FileX2 } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import {
  HScrollRow,
  FilterPill,
  PageSectionHeader,
  HeaderAction,
} from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import {
  fetchCodeProposals,
  decideCodeProposal,
  type CodeProposalDTO,
  type CodeProposalStatus,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

const STATUS_FILTERS: readonly CodeProposalStatus[] = [
  "candidate",
  "approved",
  "applied",
  "rejected",
] as const;

const RISK_STYLES: Record<string, string> = {
  low: "border-success/40 bg-success/10 text-success",
  medium: "border-warning/40 bg-warning/10 text-warning",
  high: "border-danger/40 bg-danger/10 text-danger",
  critical: "border-destructive/50 bg-destructive/15 text-destructive",
};

// Code proposals are Voyager's source-refactor counterpart to skill proposals.
// SessionEnd's source extractor watches for "boss fought the same file" and
// drafts a refactor sketch via Haiku. Approval here marks the proposal for
// the agent to attempt - the actual edit still goes through ClaudeCodeGate.
export default function CodeProposalsPage() {
  const [proposals, setProposals] = useState<CodeProposalDTO[]>([]);
  const [status, setStatus] = useState<CodeProposalStatus>("candidate");
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  async function load() {
    setLoading(true);
    const list = await fetchCodeProposals(status);
    setProposals(list ?? []);
    setLoading(false);
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status]);

  useRealtime("mem_code_proposals", load);

  async function decide(id: string, decision: "approved" | "rejected") {
    setBusy((b) => ({ ...b, [id]: true }));
    await decideCodeProposal(id, decision);
    setBusy((b) => ({ ...b, [id]: false }));
    await load();
  }

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return proposals;
    return proposals.filter((p) =>
      `${p.target_path} ${p.title} ${p.rationale} ${p.proposed_change}`
        .toLowerCase()
        .includes(q),
    );
  }, [proposals, query]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <PageSectionHeader title="code proposals" count={filtered.length}>
            <HeaderAction
              icon={<RefreshCw className="size-4" />}
              label="Refresh"
              onClick={load}
              disabled={loading}
            />
          </PageSectionHeader>

          <p className="text-xs leading-relaxed text-muted-foreground">
            Voyager drafts these when a session shows the same file fought multiple times.
            Approve to flag it for the agent - actual edits still route through Trust.
          </p>

          <div className="relative flex-1">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden
            />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter by file or title…"
              className="pl-9"
              inputMode="search"
            />
          </div>

          <HScrollRow>
            {STATUS_FILTERS.map((s) => (
              <FilterPill key={s} active={status === s} onClick={() => setStatus(s)}>
                {s}
              </FilterPill>
            ))}
          </HScrollRow>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3 sm:px-4 scroll-touch">
          {loading && (
            <p className="text-xs text-muted-foreground">Loading…</p>
          )}
          {!loading && filtered.length === 0 && (
            <EmptyState
              icon={FileX2}
              title="No proposals"
              description={
                status === "candidate"
                  ? "Voyager hasn't seen a session worth proposing a refactor for yet. Fight the same file a few times and it'll notice."
                  : "Nothing in this bucket."
              }
            />
          )}
          {!loading && filtered.length > 0 && (
            <ul className="space-y-3">
              {filtered.map((p) => (
                <ProposalRow
                  key={p.id}
                  p={p}
                  busy={!!busy[p.id]}
                  expanded={!!expanded[p.id]}
                  onToggle={() => setExpanded((m) => ({ ...m, [p.id]: !m[p.id] }))}
                  onDecide={(d) => decide(p.id, d)}
                />
              ))}
            </ul>
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function ProposalRow({
  p,
  busy,
  expanded,
  onToggle,
  onDecide,
}: {
  p: CodeProposalDTO;
  busy: boolean;
  expanded: boolean;
  onToggle: () => void;
  onDecide: (d: "approved" | "rejected") => void;
}) {
  const isPending = p.status === "candidate";
  const evidence = p.evidence as Record<string, unknown>;
  const editCount = numberField(evidence, "edit_count");
  const failureCount = numberField(evidence, "failure_count");
  const bashFailures = numberField(evidence, "bash_failures");
  const durationSec = numberField(evidence, "duration_sec");

  return (
    <li className="rounded-xl border bg-card/60 p-3 backdrop-blur-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <FileCode className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            <code className="break-all font-mono text-[11px] text-muted-foreground">
              {p.target_path || "(unspecified path)"}
            </code>
            <span
              className={cn(
                "rounded-full border px-1.5 py-0.5 font-mono text-[10px] uppercase",
                RISK_STYLES[p.risk_level] ?? RISK_STYLES.medium,
              )}
            >
              {p.risk_level}
            </span>
            <span className="rounded-full border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] uppercase text-muted-foreground">
              {p.status}
            </span>
          </div>
          <h3 className="mt-1 text-sm font-semibold leading-snug text-foreground">
            {p.title}
          </h3>
          {p.rationale && (
            <div className="mt-1.5 flex items-start gap-1 text-xs leading-relaxed text-muted-foreground">
              <Zap className="mt-0.5 size-3 shrink-0" aria-hidden />
              <span className="italic">{p.rationale}</span>
            </div>
          )}
        </div>
        {isPending && (
          <div className="flex shrink-0 gap-1">
            <Button
              size="icon"
              variant="ghost"
              className="size-11 text-success hover:bg-success/10 lg:size-9"
              onClick={() => onDecide("approved")}
              disabled={busy}
              aria-label="Approve"
            >
              <Check className="size-5 lg:size-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-11 text-muted-foreground hover:bg-destructive/10 hover:text-destructive lg:size-9"
              onClick={() => onDecide("rejected")}
              disabled={busy}
              aria-label="Reject"
            >
              <X className="size-5 lg:size-4" />
            </Button>
          </div>
        )}
      </div>

      <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-[11px] sm:grid-cols-4">
        <Stat label="edits" value={editCount} />
        <Stat label="failures" value={failureCount} />
        <Stat label="bash fails" value={bashFailures} />
        <Stat label="duration" value={durationSec != null ? `${Math.round(durationSec)}s` : null} />
      </div>

      {p.proposed_change && (
        <>
          <button
            type="button"
            onClick={onToggle}
            className="mt-2 inline-flex min-h-9 items-center gap-1 text-xs font-medium text-info hover:underline"
            aria-expanded={expanded}
          >
            <Sparkles className="size-3" aria-hidden />
            {expanded ? "Hide proposed change" : "Show proposed change"}
          </button>
          {expanded && (
            <pre className="mt-1.5 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2 font-mono text-[11px] leading-snug text-foreground">
              {p.proposed_change}
            </pre>
          )}
        </>
      )}

      {p.decision_note && (
        <p className="mt-2 text-[11px] italic text-muted-foreground">
          Note: {p.decision_note}
        </p>
      )}
    </li>
  );
}

function Stat({ label, value }: { label: string; value: number | string | null }) {
  return (
    <div className="flex items-baseline gap-1.5">
      <span className="font-mono tabular-nums text-foreground">
        {value == null ? "-" : value}
      </span>
      <span className="text-muted-foreground">{label}</span>
    </div>
  );
}

function numberField(o: Record<string, unknown>, key: string): number | null {
  const v = o?.[key];
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}
