"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, Sparkles, X } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { ModalCode, ModalDiff, ModalDl, ModalSection } from "@/components/ui/modal-content";
import { useRealtime } from "@/lib/realtime/provider";
import { useRuns } from "@/lib/runs/useRuns";
import {
  decideSkillProposal,
  fetchSkillProposals,
  fetchVoyagerStatus,
  type RunDTO,
  type SkillProposalDTO,
  type VoyagerStatusDTO,
} from "@/lib/api";

/**
 * CandidateSkillsPanel — the "Candidates" GROUP at the top of the skills
 * list, not a rail beside it.
 *
 * It used to be a `rounded-xl border bg-card/60` panel with its own collapse
 * header, its own stats strip, its own nested group headings (Merged drafts /
 * GEPA frontier / Standalone), and bordered proposal rows inside those — a
 * second surface modelling the same data shape as the skill list. Majordomo
 * §8 and CLAUDE.md's "consolidate similar surfaces" both say: fold it in.
 *
 * So this now renders exactly one `GroupLabel` + a run of `ListRow`s, which
 * the /skills list column places directly above the "Active" group. The
 * provenance that used to be a nested heading (GEPA run, merged draft,
 * standalone) moved into each row's own driver line, where it was already
 * being said — nothing was lost, one whole level of chrome went away.
 *
 * Behaviour preserved verbatim: realtime on `mem_skill_proposals`, the
 * promote/reject decide call, the Voyager status probe, and the server-tracked
 * verification run (`useRuns({kind:"skill.promote"})`) that keeps the
 * "Verifying…" state alive across navigation and shows WHY a promote failed.
 */
export function CandidateSkillsPanel({
  query,
  onCountChange,
}: {
  /** Live search text from /skills; candidates filter with the same string. */
  query?: string;
  /** Reports the visible candidate count so the page header can say it. */
  onCountChange?: (n: number) => void;
}) {
  const [proposals, setProposals] = useState<SkillProposalDTO[]>([]);
  const [status, setStatus] = useState<VoyagerStatusDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<SkillProposalDTO | null>(null);

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
  // mem_runs row. Read it via useRuns so each row shows a live "verifying…"
  // spinner that survives navigation/refresh, and a failed verification shows
  // WHY — instead of the row just sitting there. (CLAUDE.md → "Server-tracked
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

  const offline = Boolean(status && !status.enabled);

  // The same search string that filters the skill list filters candidates,
  // so a query never leaves a stale group at the top of the column.
  const visible = useMemo(() => {
    const q = (query ?? "").trim().toLowerCase();
    const sorted = sortProposals(proposals);
    if (!q) return sorted;
    return sorted.filter((p) =>
      `${p.parent_skill ?? ""} ${p.name} ${p.description ?? ""} ${p.reasoning ?? ""}`
        .toLowerCase()
        .includes(q),
    );
  }, [proposals, query]);

  useEffect(() => {
    onCountChange?.(visible.length);
  }, [visible.length, onCountChange]);

  // Nothing to say and nothing wrong: stay silent rather than leave an empty
  // group label sitting above the list (feedback: no empty rows in the UI).
  if (!loading && !offline && visible.length === 0) return null;

  return (
    <div className="min-w-0">
      <GroupLabel
        label="Candidates"
        count={visible.length || undefined}
        trailing={
          <span className="font-mono text-[11px] tabular-nums text-quiet">
            {loading
              ? "checking…"
              : offline
                ? "Voyager off"
                : status
                  ? `${status.open_sessions} open · ${status.tracked_triplets} triplets`
                  : ""}
          </span>
        }
      />

      {offline ? (
        <p className="py-2 text-[12.5px] leading-relaxed text-quiet">
          Voyager is off, so nothing is being proposed. Set{" "}
          <code className="font-mono text-[12px]">INFINITY_VOYAGER=true</code> on core to enable
          the auto-skill loop.
        </p>
      ) : loading && visible.length === 0 ? (
        <p className="py-2 text-[12.5px] text-quiet">Checking for candidates…</p>
      ) : (
        visible.map((p) => (
          <CandidateRow
            key={p.id}
            proposal={p}
            busy={!!busy[p.id]}
            run={runByProposal.get(p.id) ?? null}
            onOpen={() => setSelected(p)}
            onDecide={decide}
          />
        ))
      )}

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
    </div>
  );
}

function CandidateRow({
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
  // so the boss sees WHY the skill wasn't promoted instead of a silent row.
  const verifying = busy || run?.status === "running";
  const failed = run?.status === "error";
  const target = proposal.parent_skill || proposal.name;
  const title = proposal.parent_skill ? `Revise ${target}` : `New skill: ${target}`;
  const meta = [driverLine(proposal), `${proposal.risk_level} risk`]
    .concat(
      (proposal.conflicts?.length ?? 0) > 0 ? [`${proposal.conflicts?.length} conflicts`] : [],
    )
    .join(" · ");

  return (
    <ListRow
      tone={failed ? "danger" : "warning"}
      live={verifying}
      // The row itself is NOT the tappable element: it carries two action
      // buttons, and a <button> may not contain a <button>. The title is the
      // tap target that opens the full proposal instead.
      title={
        <button
          type="button"
          onClick={onOpen}
          className="block w-full min-w-0 truncate py-1 text-left transition-colors hover:text-info"
        >
          {title}
        </button>
      }
      meta={meta}
      chevron={false}
      trailing={
        verifying ? (
          <span
            className="inline-flex items-center gap-1.5 text-[12px] font-medium text-quiet"
            aria-live="polite"
          >
            <Spinner className="size-4" aria-hidden />
            {run?.progress_label || "Verifying…"}
          </span>
        ) : (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="size-11 text-success hover:bg-success/10 lg:size-9"
              onClick={() => onDecide(proposal.id, "promoted")}
              aria-label={`Promote ${target}`}
            >
              <Check className="size-5 lg:size-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-11 text-quiet hover:bg-danger/10 hover:text-danger lg:size-9"
              onClick={() => onDecide(proposal.id, "rejected")}
              aria-label={`Reject ${target}`}
            >
              <X className="size-5 lg:size-4" />
            </Button>
          </>
        )
      }
    >
      {/* A failed verification is the boss's signal that the skill didn't run
          clean — say why, right where the buttons were, so it never just
          "stays there" with no explanation. The buttons re-appear so he can
          retry or reject. */}
      {failed ? (
        <p className="min-w-0 max-w-full whitespace-pre-wrap text-[12.5px] leading-relaxed text-danger [overflow-wrap:anywhere]">
          {run?.human_error?.title ||
            run?.error ||
            "Verification failed — the skill didn't run clean, so it wasn't promoted."}
        </p>
      ) : null}
    </ListRow>
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
            <Spinner className="size-4" aria-hidden />
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
        <div className="mb-3 min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere] rounded-[10px] bg-danger/10 px-3 py-2 text-[12.5px] text-danger">
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
              <div key={i} className="rounded-[10px] bg-muted px-3 py-2 text-[12px]">
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

/**
 * sortProposals keeps the old grouping's ORDER (merged drafts first, then
 * each GEPA frontier by pareto rank, then standalone) without the nested
 * headings that order used to need. Each row says its own provenance in
 * `driverLine`, so the sequence is the only thing the grouping still buys.
 */
function sortProposals(proposals: SkillProposalDTO[]): SkillProposalDTO[] {
  const rank = (p: SkillProposalDTO) =>
    p.proposal_kind === "draft" ? 0 : p.frontier_run_id ? 1 : 2;
  return [...proposals].sort((a, b) => {
    const r = rank(a) - rank(b);
    if (r !== 0) return r;
    if (a.frontier_run_id && b.frontier_run_id && a.frontier_run_id !== b.frontier_run_id) {
      return a.frontier_run_id.localeCompare(b.frontier_run_id);
    }
    return (a.pareto_rank || 999) - (b.pareto_rank || 999);
  });
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
