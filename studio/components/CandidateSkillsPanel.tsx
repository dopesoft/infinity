"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, ChevronDown, GitCompare, Layers, Loader2, Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { ModalCode, ModalDiff, ModalDl, ModalSection } from "@/components/ui/modal-content";
import { useRealtime } from "@/lib/realtime/provider";
import { useRuns } from "@/lib/runs/useRuns";
import { cn } from "@/lib/utils";
import {
  decideSkillProposal,
  fetchSkillProposals,
  fetchVoyagerStatus,
  type RunDTO,
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

  // Promote is a long action: the server RUNS the skill's verification harness
  // (an ephemeral LLM session, up to ~90s) before promoting, booked as a
  // mem_runs row. Read it via useRuns so each card shows a live "verifying…"
  // spinner that survives navigation/refresh, and a failed verification shows
  // WHY — instead of the card just sitting there. (CLAUDE.md → "Server-tracked
  // progress".)
  const { runs: promoteRuns } = useRuns({ kind: "skill.promote", limit: 100 });
  const runByProposal = useMemo(() => {
    const m = new Map<string, RunDTO>();
    for (const r of promoteRuns) if (!m.has(r.target_id)) m.set(r.target_id, r); // newest-first
    return m;
  }, [promoteRuns]);

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
                      run={runByProposal.get(p.id) ?? null}
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
          run={runByProposal.get(selected.id) ?? null}
          onDecide={decide}
        />
      )}
    </section>
  );
}

function ProposalRow({
  proposal,
  busy,
  run,
  onOpen,
  onDecide,
}: {
  proposal: SkillProposalDTO;
  busy: boolean;
  run: RunDTO | null;
  onOpen: () => void;
  onDecide: (id: string, decision: "promoted" | "rejected") => void;
}) {
  // verifying covers the click→server gap (busy) and the live harness run
  // (run.status==='running'); failed surfaces a verification that didn't pass
  // so the boss sees WHY the skill wasn't promoted instead of a silent card.
  const verifying = busy || run?.status === "running";
  const failed = run?.status === "error";
  return (
    <div className="rounded-lg border bg-background/40 p-2.5">
      <div className="flex items-start justify-between gap-2">
        <button type="button" onClick={onOpen} className="min-w-0 flex-1 text-left">
          <div className="flex flex-wrap items-center gap-1.5">
            {/* Lead with what it IS in plain words — revision vs new skill —
                so a row never reads like a duplicate skill. */}
            <span
              className={cn(
                "shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide",
                proposal.parent_skill
                  ? "bg-info/15 text-info"
                  : "bg-success/15 text-success",
              )}
            >
              {proposal.parent_skill ? "Revises" : "New"}
            </span>
            <code className="break-all font-mono text-sm font-semibold text-foreground lg:text-xs">
              {proposal.parent_skill || proposal.name}
            </code>
            <span className={cn("rounded-full border px-1.5 py-0.5 text-[10px] font-mono uppercase", RISK_STYLES[proposal.risk_level] ?? RISK_STYLES.low)}>
              risk {proposal.risk_level}
            </span>
            {(proposal.conflicts?.length ?? 0) > 0 && (
              <span className="rounded-full bg-warning/15 px-1.5 py-0.5 font-mono text-[10px] text-warning">
                {proposal.conflicts?.length} conflicts
              </span>
            )}
          </div>
          {/* What DROVE this + when — so an inline approve isn't blind.
              The boss kept approving from the row without seeing why. */}
          <p className="mt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/80">
            {driverLine(proposal)}
          </p>
          {/* The actual WHY (reasoning), not the generic skill description. */}
          <p className="mt-0.5 line-clamp-3 text-xs leading-snug text-muted-foreground lg:text-[11px]">
            {proposal.reasoning || proposal.description || "No rationale recorded."}
          </p>
          <p className="mt-1 text-[10px] text-muted-foreground/70">
            {proposal.parent_skill
              ? "Tap to see exactly what changes vs the live version."
              : "Tap to read the full skill before installing."}
          </p>
        </button>
        <div className="flex shrink-0 items-center gap-1">
          {verifying ? (
            <span className="inline-flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-muted-foreground" aria-live="polite">
              <Loader2 className="size-4 animate-spin" aria-hidden />
              {run?.progress_label || "Verifying…"}
            </span>
          ) : (
            <>
              <Button size="icon" variant="ghost" className="size-11 text-success hover:bg-success/10 lg:size-9" onClick={() => onDecide(proposal.id, "promoted")} aria-label="Promote">
                <Check className="size-5 lg:size-4" />
              </Button>
              <Button size="icon" variant="ghost" className="size-11 text-muted-foreground hover:bg-destructive/10 hover:text-destructive lg:size-9" onClick={() => onDecide(proposal.id, "rejected")} aria-label="Reject">
                <X className="size-5 lg:size-4" />
              </Button>
            </>
          )}
        </div>
      </div>
      {/* A failed verification is the boss's signal that the skill didn't run
          clean — show why, right where the buttons were, so it never just
          "stays there" with no explanation. The buttons re-appear so he can
          retry or reject. */}
      {failed && (
        <p className="mt-2 min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere] rounded-md border border-danger/40 bg-danger/5 px-2 py-1 text-[11px] text-danger">
          {run?.human_error?.title || run?.error || "Verification failed — the skill didn't run clean, so it wasn't promoted."}
        </p>
      )}
    </div>
  );
}

function ProposalModal({
  proposal,
  open,
  onOpenChange,
  busy,
  run,
  onDecide,
}: {
  proposal: SkillProposalDTO;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  busy: boolean;
  run: RunDTO | null;
  onDecide: (id: string, decision: "promoted" | "rejected") => void;
}) {
  const verifying = busy || run?.status === "running";
  const failed = run?.status === "error";
  const isRevision = !!proposal.parent_skill;
  const targetName = proposal.parent_skill || proposal.name;
  const replacesVersion = proposal.parent_active_version || proposal.parent_version;
  // Plain-English "what is this" — no GEPA jargon up top.
  const whatHappens = isRevision
    ? `This rewrites your existing skill “${targetName}”. Approving replaces${
        replacesVersion ? ` its live version (${replacesVersion})` : " the live version"
      } with this one. It does not create a second skill.`
    : `This is a brand-new skill, “${targetName}”. Approving installs it and Jarvis can use it right away.`;
  const howFound = proposal.frontier_run_id
    ? "Generated by a GEPA optimization run (the system tried variations and scored them)."
    : proposal.proposal_kind === "draft"
      ? `Drafted by Jarvis after watching this skill${(proposal.revision ?? 1) > 1 ? ` across ${proposal.revision} sessions` : ""}.`
      : "Proposed by Jarvis from a session.";

  // Jargon demoted to a collapsed "Technical details" footer.
  const detailEntries = [
    { k: "execution risk", v: proposal.risk_level },
    { k: "importance", v: `${proposal.importance ?? 50}${proposal.importance_reason ? ` — ${proposal.importance_reason}` : ""}` },
    ...(proposal.frontier_run_id
      ? [
          { k: "GEPA run", v: proposal.frontier_run_id.slice(0, 8) },
          { k: "score", v: proposal.score ? formatScore(proposal.score) : "-" },
          { k: "pareto rank", v: proposal.pareto_rank || "-" },
        ]
      : []),
    { k: "created", v: new Date(proposal.created_at).toLocaleString() },
  ];
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={targetName}
      header={
        <ResponsiveModalHeader
          icon={<Sparkles className="size-4" />}
          eyebrow={isRevision ? "Revision to an existing skill" : "Brand-new skill"}
          title={targetName}
          subtitle={isRevision && replacesVersion ? `Replaces ${replacesVersion}` : undefined}
        />
      }
      footer={
        verifying ? (
          <span className="inline-flex items-center gap-2 px-1 text-sm font-medium text-muted-foreground" aria-live="polite">
            <Loader2 className="size-4 animate-spin" aria-hidden />
            {run?.progress_label || "Verifying — running the skill to confirm it works…"}
          </span>
        ) : (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>Close</Button>
            <Button variant="outline" onClick={() => onDecide(proposal.id, "rejected")}>
              <X className="size-4" /> Reject
            </Button>
            <Button onClick={() => onDecide(proposal.id, "promoted")}>
              <Check className="size-4" /> {isRevision ? "Approve & replace" : "Approve & install"}
            </Button>
          </>
        )
      }
    >
      {/* If the last promote attempt failed verification, lead with WHY — the
          skill ran but didn't return clean data, so it wasn't installed. */}
      {failed && (
        <div className="mb-3 min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere] rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
          <span className="font-medium">Didn’t pass verification — not installed. </span>
          {run?.human_error?.title || run?.error || "The skill ran but didn’t return clean data. Adjust it and try again."}
        </div>
      )}

      {/* 1. What's happening — plain language, first thing. */}
      <ModalSection label="What this does">
        <p className="break-words text-[13px] leading-relaxed text-foreground/90">{whatHappens}</p>
        <p className="mt-1.5 text-[11px] leading-relaxed text-muted-foreground">{howFound}</p>
      </ModalSection>

      {/* 2. Why. */}
      {proposal.reasoning && (
        <ModalSection label="Why Jarvis proposed it">
          <p className="break-words text-[13px] leading-relaxed text-foreground/85">{proposal.reasoning}</p>
        </ModalSection>
      )}

      {/* 3. What's changing — the actual diff (or the full new body for a new skill). */}
      <ModalSection
        label={isRevision ? "What's changing" : "The skill"}
        meta={isRevision ? "before → after" : undefined}
      >
        {isRevision ? (
          <ModalDiff before={proposal.parent_body ?? ""} after={proposal.skill_md} />
        ) : (
          <ModalCode>{proposal.skill_md}</ModalCode>
        )}
      </ModalSection>

      {/* 4. Conflicts (only when present). */}
      {(proposal.conflicts?.length ?? 0) > 0 && (
        <ModalSection label="Heads up — conflicts" tone="warning">
          <ul className="space-y-1 text-[12px] text-foreground/85">
            {proposal.conflicts?.map((c, i) => <li key={`${c}-${i}`} className="break-words">{c}</li>)}
          </ul>
        </ModalSection>
      )}

      {/* 5. Change history (only when present). */}
      {(proposal.changes_log?.length ?? 0) > 0 && (
        <ModalSection label="Change history" meta={`${proposal.changes_log?.length ?? 0} entries`}>
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

      {/* 6. The full body, for a revision (the diff is primary; this is reference). */}
      {isRevision && proposal.skill_md && (
        <ModalSection label="Full proposed SKILL.md">
          <ModalCode>{proposal.skill_md}</ModalCode>
        </ModalSection>
      )}

      {/* 7. Technical details — demoted to the bottom, not fluff up top. */}
      <ModalSection label="Technical details">
        <ModalDl entries={detailEntries} />
      </ModalSection>
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

// driverLine answers "what drove this proposal, and when" in one glance, so an
// inline approve isn't blind. Drafts accrue across sessions (revision = how
// many times the extractor merged into it); frontier candidates come from a
// GEPA optimization run; standalone are one-off proposals.
function driverLine(p: SkillProposalDTO): string {
  const when = relTime(p.created_at);
  if (p.frontier_run_id) {
    return `GEPA optimization${typeof p.score === "number" ? ` · scored ${formatScore(p.score)}` : ""} · ${when}`;
  }
  if (p.proposal_kind === "draft") {
    const rev = p.revision ?? 1;
    return rev > 1
      ? `Merged from ${rev} sessions · updated ${when}`
      : `Drafted from a session · ${when}`;
  }
  return `Proposed ${when}`;
}

function relTime(iso?: string): string {
  if (!iso) return "unknown time";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "unknown time";
  const secs = Math.max(1, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.round(hrs / 24);
  return `${days}d ago`;
}
