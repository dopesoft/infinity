"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, ChevronDown, GitCompare, Layers, Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { ModalCode, ModalDl, ModalSection } from "@/components/ui/modal-content";
import { useRealtime } from "@/lib/realtime/provider";
import { cn } from "@/lib/utils";
import {
  decideSkillProposal,
  fetchSkillProposals,
  fetchVoyagerStatus,
  type SkillProposalDTO,
  type VoyagerStatusDTO,
} from "@/lib/api";

const RISK_STYLES: Record<string, string> = {
  low: "border-success/40 bg-success/10 text-success",
  medium: "border-warning/40 bg-warning/10 text-warning",
  high: "border-danger/40 bg-danger/10 text-danger",
  critical: "border-destructive/50 bg-destructive/15 text-destructive",
};

type Group =
  | { kind: "draft"; key: string; title: string; items: SkillProposalDTO[] }
  | { kind: "frontier"; key: string; title: string; items: SkillProposalDTO[] }
  | { kind: "standalone"; key: string; title: string; items: SkillProposalDTO[] };

export function CandidateSkillsPanel() {
  const [proposals, setProposals] = useState<SkillProposalDTO[]>([]);
  const [status, setStatus] = useState<VoyagerStatusDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [collapsed, setCollapsed] = useState<boolean | null>(null);
  const [selected, setSelected] = useState<SkillProposalDTO | null>(null);

  useEffect(() => {
    setCollapsed(window.matchMedia("(min-width: 1024px)").matches ? false : true);
  }, []);

  async function load() {
    setLoading(true);
    const [props, st] = await Promise.all([fetchSkillProposals("candidate"), fetchVoyagerStatus()]);
    setProposals(props ?? []);
    setStatus(st);
    setLoading(false);
  }

  useEffect(() => {
    load();
  }, []);

  useRealtime("mem_skill_proposals", load);

  async function decide(id: string, decision: "promoted" | "rejected") {
    setBusy((b) => ({ ...b, [id]: true }));
    await decideSkillProposal(id, decision);
    setBusy((b) => ({ ...b, [id]: false }));
    await load();
  }

  const groups = useMemo(() => groupProposals(proposals), [proposals]);
  const offline = status && !status.enabled;

  return (
    <section className="rounded-xl border bg-card/60 backdrop-blur-sm">
      <header className="flex items-center gap-1 border-b pr-1">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex min-h-12 min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left lg:min-h-0 lg:cursor-default lg:py-2"
          aria-expanded={!collapsed}
        >
          <Sparkles className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          <span className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            Candidate skills
          </span>
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                className={cn(
                  "inline-block size-1.5 shrink-0 rounded-full",
                  loading
                    ? "bg-muted-foreground/40"
                    : offline
                      ? "bg-danger"
                      : status?.status && status.status !== "running"
                        ? "bg-warning"
                        : "bg-success",
                )}
                aria-label="Voyager status"
              />
            </TooltipTrigger>
            <TooltipContent side="top" align="start">
              <div className="space-y-0.5">
                <div className="font-medium">
                  {loading
                    ? "Checking Voyager..."
                    : offline
                      ? "Voyager off"
                      : `Voyager · ${status?.status ?? "unknown"}`}
                </div>
                {status && !offline ? (
                  <div className="font-mono text-[11px] text-muted-foreground">
                    {status.open_sessions} open · {status.tracked_triplets} triplets
                  </div>
                ) : null}
              </div>
            </TooltipContent>
          </Tooltip>
          {proposals.length > 0 && (
            <span className="rounded-full bg-info/15 px-1.5 py-0.5 font-mono text-[10px] text-info">
              {proposals.length}
            </span>
          )}
        </button>
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex size-11 items-center justify-center lg:hidden"
          aria-label={collapsed ? "Expand" : "Collapse"}
        >
          <ChevronDown
            className={cn("size-4 text-muted-foreground transition-transform", !collapsed && "rotate-180")}
            aria-hidden
          />
        </button>
      </header>

      <div className={cn(collapsed && "hidden lg:block")}>
        {status && !offline && (
          <div className="grid grid-cols-2 gap-2 border-b px-3 py-2 text-[11px]">
            <div className="flex items-baseline gap-1.5">
              <span className="font-mono tabular-nums text-foreground">{status.open_sessions}</span>
              <span className="text-muted-foreground">open sessions</span>
            </div>
            <div className="flex items-baseline gap-1.5">
              <span className="font-mono tabular-nums text-foreground">{status.tracked_triplets}</span>
              <span className="text-muted-foreground">tracked triplets</span>
            </div>
          </div>
        )}

        <div className="space-y-3 p-3">
          {offline && (
            <p className="text-xs text-muted-foreground">
              Voyager is off. Set <code className="font-mono text-[10px]">INFINITY_VOYAGER=true</code>{" "}
              on core to enable the auto-skill loop.
            </p>
          )}
          {!offline && loading && <p className="text-xs text-muted-foreground">Loading...</p>}
          {!offline && !loading && proposals.length === 0 && (
            <p className="text-xs text-muted-foreground">
              No candidates yet. Voyager proposes after meaningful sessions, repeated tool paths, or repair loops.
            </p>
          )}
          {!offline &&
            groups.map((g) => (
              <div key={g.key} className="space-y-2">
                <div className="flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  {g.kind === "frontier" ? <GitCompare className="size-3" /> : <Layers className="size-3" />}
                  <span>{g.title}</span>
                  <span className="rounded-full bg-muted px-1.5 py-0.5 font-mono">{g.items.length}</span>
                </div>
                <div className={cn("grid gap-2", g.kind === "frontier" && "lg:grid-cols-2")}>
                  {g.items.map((p) => (
                    <ProposalRow
                      key={p.id}
                      proposal={p}
                      busy={!!busy[p.id]}
                      onOpen={() => setSelected(p)}
                      onDecide={decide}
                    />
                  ))}
                </div>
              </div>
            ))}
        </div>
      </div>

      {selected && (
        <ProposalModal
          proposal={selected}
          open={!!selected}
          onOpenChange={(open) => !open && setSelected(null)}
          busy={!!busy[selected.id]}
          onDecide={decide}
        />
      )}
    </section>
  );
}

function ProposalRow({
  proposal,
  busy,
  onOpen,
  onDecide,
}: {
  proposal: SkillProposalDTO;
  busy: boolean;
  onOpen: () => void;
  onDecide: (id: string, decision: "promoted" | "rejected") => void;
}) {
  return (
    <div className="rounded-lg border bg-background/40 p-2.5">
      <div className="flex items-start justify-between gap-2">
        <button type="button" onClick={onOpen} className="min-w-0 flex-1 text-left">
          <div className="flex flex-wrap items-center gap-1.5">
            <code className="break-all font-mono text-sm font-semibold text-foreground lg:text-xs">
              {proposal.parent_skill || proposal.name}
            </code>
            <span className={cn("rounded-full border px-1.5 py-0.5 text-[10px] font-mono uppercase", RISK_STYLES[proposal.risk_level] ?? RISK_STYLES.low)}>
              {proposal.risk_level}
            </span>
            {proposal.proposal_kind === "draft" && (
              <span className="rounded-full bg-info/15 px-1.5 py-0.5 font-mono text-[10px] text-info">
                r{proposal.revision ?? 1}
              </span>
            )}
            {proposal.frontier_run_id && (
              <span className="rounded-full bg-tier-procedural/15 px-1.5 py-0.5 font-mono text-[10px] text-tier-procedural">
                rank {proposal.pareto_rank || "-"} · {formatScore(proposal.score)}
              </span>
            )}
            {(proposal.conflicts?.length ?? 0) > 0 && (
              <span className="rounded-full bg-warning/15 px-1.5 py-0.5 font-mono text-[10px] text-warning">
                {proposal.conflicts?.length} conflicts
              </span>
            )}
          </div>
          <p className="mt-1 line-clamp-3 text-xs leading-snug text-muted-foreground lg:text-[11px]">
            {proposal.description || proposal.reasoning}
          </p>
        </button>
        <div className="flex shrink-0 gap-1">
          <Button size="icon" variant="ghost" className="size-11 text-success hover:bg-success/10 lg:size-9" onClick={() => onDecide(proposal.id, "promoted")} disabled={busy} aria-label="Promote">
            <Check className="size-5 lg:size-4" />
          </Button>
          <Button size="icon" variant="ghost" className="size-11 text-muted-foreground hover:bg-destructive/10 hover:text-destructive lg:size-9" onClick={() => onDecide(proposal.id, "rejected")} disabled={busy} aria-label="Reject">
            <X className="size-5 lg:size-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}

function ProposalModal({
  proposal,
  open,
  onOpenChange,
  busy,
  onDecide,
}: {
  proposal: SkillProposalDTO;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  busy: boolean;
  onDecide: (id: string, decision: "promoted" | "rejected") => void;
}) {
  const entries = [
    { k: "kind", v: proposal.proposal_kind || (proposal.frontier_run_id ? "frontier" : "standalone") },
    { k: "revision", v: proposal.revision ?? 1 },
    { k: "parent", v: proposal.parent_skill || "-" },
    { k: "frontier", v: proposal.frontier_run_id || "-" },
    { k: "score", v: proposal.score ? formatScore(proposal.score) : "-" },
    { k: "rank", v: proposal.pareto_rank || "-" },
    { k: "created", v: new Date(proposal.created_at).toLocaleString() },
  ];
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={proposal.name}
      header={
        <ResponsiveModalHeader
          icon={<Sparkles className="size-4" />}
          eyebrow={proposal.frontier_run_id ? "GEPA frontier candidate" : proposal.proposal_kind === "draft" ? "Merged draft" : "Candidate skill"}
          title={proposal.parent_skill || proposal.name}
          subtitle={proposal.description}
        />
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Close</Button>
          <Button variant="outline" onClick={() => onDecide(proposal.id, "rejected")} disabled={busy}>
            <X className="size-4" /> Reject
          </Button>
          <Button onClick={() => onDecide(proposal.id, "promoted")} disabled={busy}>
            <Check className="size-4" /> Promote
          </Button>
        </>
      }
    >
      <ModalSection label="Metadata">
        <ModalDl entries={entries} />
      </ModalSection>
      {proposal.reasoning && (
        <ModalSection label="Reasoning">
          <p className="break-words text-[13px] text-foreground/85">{proposal.reasoning}</p>
        </ModalSection>
      )}
      {(proposal.conflicts?.length ?? 0) > 0 && (
        <ModalSection label="Conflicts" tone="warning">
          <ul className="space-y-1 text-[12px] text-foreground/85">
            {proposal.conflicts?.map((c, i) => <li key={`${c}-${i}`} className="break-words">{c}</li>)}
          </ul>
        </ModalSection>
      )}
      {(proposal.changes_log?.length ?? 0) > 0 && (
        <ModalSection label="Changes" meta={`${proposal.changes_log?.length ?? 0} entries`}>
          <div className="space-y-2">
            {proposal.changes_log?.map((c, i) => (
              <div key={i} className="rounded-md border bg-muted/20 p-2 text-[12px]">
                <div className="font-mono text-[10px] uppercase text-muted-foreground">{String(c.source ?? "change")} · {String(c.at ?? "")}</div>
                <div className="mt-1 break-words text-foreground/85">{String(c.reasoning ?? c.description ?? "")}</div>
              </div>
            ))}
          </div>
        </ModalSection>
      )}
      {proposal.skill_md && (
        <ModalSection label="SKILL.md">
          <ModalCode>{proposal.skill_md}</ModalCode>
        </ModalSection>
      )}
    </ResponsiveModal>
  );
}

function groupProposals(proposals: SkillProposalDTO[]): Group[] {
  const drafts = proposals.filter((p) => p.proposal_kind === "draft");
  const frontierMap = new Map<string, SkillProposalDTO[]>();
  const standalone: SkillProposalDTO[] = [];
  for (const p of proposals) {
    if (p.proposal_kind === "draft") continue;
    if (p.frontier_run_id) {
      const rows = frontierMap.get(p.frontier_run_id) ?? [];
      rows.push(p);
      frontierMap.set(p.frontier_run_id, rows);
    } else {
      standalone.push(p);
    }
  }
  const groups: Group[] = [];
  if (drafts.length) groups.push({ kind: "draft", key: "drafts", title: "Merged drafts", items: drafts });
  for (const [id, items] of frontierMap) {
    items.sort((a, b) => (a.pareto_rank || 999) - (b.pareto_rank || 999));
    groups.push({ kind: "frontier", key: id, title: `GEPA frontier ${id.slice(0, 8)}`, items });
  }
  if (standalone.length) groups.push({ kind: "standalone", key: "standalone", title: "Standalone candidates", items: standalone });
  return groups;
}

function formatScore(score?: number) {
  if (typeof score !== "number") return "-";
  return score.toFixed(score > 1 ? 1 : 2);
}
