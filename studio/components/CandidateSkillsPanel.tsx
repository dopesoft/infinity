"use client";

import { useEffect, useState } from "react";
import { Zap, Check, ChevronDown, Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
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

// CandidateSkillsPanel — surfaces Voyager's auto-proposed skills, lets the
// boss promote or reject. Pure UI; Voyager generates these from SessionEnd
// extraction + real-time discovery.
export function CandidateSkillsPanel() {
  const [proposals, setProposals] = useState<SkillProposalDTO[]>([]);
  const [status, setStatus] = useState<VoyagerStatusDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  // Per-proposal description expand state. Click the description to read
  // the full text without it being clamped at 2-3 lines.
  const [descOpen, setDescOpen] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [collapsed, setCollapsed] = useState<boolean | null>(null);

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

  // Auto-refresh when Voyager or the agent (via skill_propose / skill_optimize
  // tools) inserts new candidates. mem_skill_proposals is broadcast on the
  // Supabase realtime channel set up in migration 008_realtime.
  useRealtime("mem_skill_proposals", load);

  async function decide(id: string, decision: "promoted" | "rejected") {
    setBusy((b) => ({ ...b, [id]: true }));
    await decideSkillProposal(id, decision);
    setBusy((b) => ({ ...b, [id]: false }));
    await load();
  }

  const offline = status && !status.enabled;

  return (
    <section className="rounded-xl border bg-card/60 backdrop-blur-sm">
      <header className="flex items-center gap-1 border-b pr-1">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex min-h-12 min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left lg:min-h-0 lg:py-2 lg:cursor-default"
          aria-expanded={!collapsed}
        >
          <Sparkles className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          <span className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            Candidate skills
          </span>
          {/* Status dot — green if Voyager is running, yellow if enabled
              but in a non-running state (starting/idle/paused), red if
              disabled entirely. Hover reveals the live status string + the
              open-sessions / tracked-triplets counters that used to live in
              the verbose chip. */}
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
                    ? "Checking Voyager…"
                    : offline
                      ? "Voyager off"
                      : `Voyager · ${status?.status ?? "unknown"}`}
                </div>
                {offline ? (
                  <div className="text-[11px] text-muted-foreground">
                    Set <code className="font-mono text-[10px]">INFINITY_VOYAGER=true</code> on core
                    to enable the auto-skill loop.
                  </div>
                ) : status ? (
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

        {/* Chevron — rightmost. Mobile-only toggle. */}
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex size-11 items-center justify-center lg:hidden"
          aria-label={collapsed ? "Expand" : "Collapse"}
        >
          <ChevronDown
            className={cn(
              "size-4 text-muted-foreground transition-transform",
              !collapsed && "rotate-180",
            )}
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
            <span className="font-mono tabular-nums text-foreground">
              {status.tracked_triplets}
            </span>
            <span className="text-muted-foreground">tracked triplets</span>
          </div>
        </div>
      )}

      <div className="space-y-2 p-3">
        {offline && (
          <p className="text-xs text-muted-foreground">
            Voyager is off. Set <code className="font-mono text-[10px]">INFINITY_VOYAGER=true</code>{" "}
            on core to enable the auto-skill loop.
          </p>
        )}
        {!offline && loading && <p className="text-xs text-muted-foreground">Loading…</p>}
        {!offline && !loading && proposals.length === 0 && (
          <p className="text-xs text-muted-foreground">
            No candidates yet. Voyager proposes after a meaningful session ends or a tool sequence
            repeats across sessions.
          </p>
        )}
        {!offline &&
          proposals.map((p) => {
            const isOpen = !!open[p.id];
            return (
              <div
                key={p.id}
                className="rounded-lg border bg-background/40 p-2.5"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <code className="break-all font-mono text-sm font-semibold text-foreground lg:text-xs">
                        {p.name}
                      </code>
                      <span
                        className={cn(
                          "rounded-full border px-1.5 py-0.5 text-[10px] font-mono uppercase",
                          RISK_STYLES[p.risk_level] ?? RISK_STYLES.low,
                        )}
                      >
                        {p.risk_level}
                      </span>
                    </div>
                    <button
                      type="button"
                      onClick={() => setDescOpen((m) => ({ ...m, [p.id]: !m[p.id] }))}
                      className={cn(
                        "mt-1 block w-full cursor-pointer text-left text-xs leading-snug text-muted-foreground lg:text-[11px]",
                        descOpen[p.id]
                          ? "whitespace-pre-wrap break-words"
                          : "line-clamp-3 lg:line-clamp-2",
                      )}
                      aria-expanded={!!descOpen[p.id]}
                      aria-label={descOpen[p.id] ? "Collapse description" : "Expand description"}
                    >
                      {p.description}
                    </button>
                  </div>
                  <div className="flex shrink-0 gap-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-11 text-success hover:bg-success/10 lg:size-9"
                      onClick={() => decide(p.id, "promoted")}
                      disabled={!!busy[p.id]}
                      aria-label="Promote"
                    >
                      <Check className="size-5 lg:size-4" />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-11 text-muted-foreground hover:bg-destructive/10 hover:text-destructive lg:size-9"
                      onClick={() => decide(p.id, "rejected")}
                      disabled={!!busy[p.id]}
                      aria-label="Reject"
                    >
                      <X className="size-5 lg:size-4" />
                    </Button>
                  </div>
                </div>

                {p.reasoning && (
                  <div className="mt-1.5 flex items-start gap-1 text-[11px] text-muted-foreground">
                    <Zap className="mt-0.5 size-3 shrink-0" aria-hidden />
                    <span className="italic">{p.reasoning}</span>
                  </div>
                )}

                {p.skill_md && (
                  <button
                    type="button"
                    onClick={() => setOpen((o) => ({ ...o, [p.id]: !o[p.id] }))}
                    className="mt-2 inline-flex min-h-11 items-center text-xs font-medium text-info hover:underline lg:min-h-0 lg:text-[11px]"
                  >
                    {isOpen ? "Hide draft" : "Show drafted SKILL.md"}
                  </button>
                )}
                {isOpen && p.skill_md && (
                  <pre className="mt-1.5 max-h-64 overflow-auto rounded-md bg-muted/40 p-2 font-mono text-[10px] leading-snug">
                    {p.skill_md}
                  </pre>
                )}
              </div>
            );
          })}
      </div>
      </div>
    </section>
  );
}
