"use client";

import { useEffect, useState } from "react";
import { Check, ChevronDown, ChevronRight, ClipboardList, ShieldCheck, Flag, X } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import type { ChatMessage } from "@/hooks/useChat";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ToolCallCard } from "@/components/ToolCallCard";
import { approvePlan, discardPlan, fetchPlan } from "@/lib/api";
import { cn } from "@/lib/utils";
import { toast } from "@/components/ui/sonner";

/**
 * PlanProposalCard - the "here's what I'd do, say go" surface.
 *
 * When plan_create runs while the boss is talking something through (or the
 * turn wasn't a clear work order), Core creates the plan as a PROPOSAL: not
 * executable, not in the dock, never resumed by a later session. This card
 * renders that proposal inline in chat with two decisions:
 *   • Go ahead  → POST /api/plans/approve + a chat message so Jarvis starts.
 *   • Not yet   → POST /api/plans/discard + a chat message to keep talking.
 * A plan_create that produced an ACTIVE plan (a real work order) falls
 * through to the generic ToolCallCard, exactly as before.
 */

type ProposedStep = {
  id?: string;
  title: string;
  detail?: string;
  is_checkpoint?: boolean;
  verify_required?: boolean;
};

type ProposedPlan = {
  id: string;
  title: string;
  goal?: string;
  status: string;
  steps: ProposedStep[];
};

function parseProposal(output?: string): ProposedPlan | null {
  if (!output) return null;
  try {
    const v = JSON.parse(output) as { plan?: Record<string, unknown> };
    const p = v?.plan;
    if (!p || typeof p !== "object") return null;
    if (p.status !== "proposed") return null;
    const rawSteps = Array.isArray(p.steps) ? (p.steps as Record<string, unknown>[]) : [];
    return {
      id: String(p.id ?? ""),
      title: String(p.title ?? "Plan"),
      goal: typeof p.goal === "string" ? p.goal : undefined,
      status: String(p.status),
      steps: rawSteps
        .map((s) => ({
          id: typeof s.id === "string" ? s.id : undefined,
          title: String(s.title ?? ""),
          detail: typeof s.detail === "string" ? s.detail : undefined,
          is_checkpoint: !!s.is_checkpoint,
          verify_required: !!s.verify_required,
        }))
        .filter((s) => s.title),
    };
  } catch {
    return null;
  }
}

/** Tool calls whose result can carry a proposed plan worth a decision card. */
export const PLAN_PROPOSAL_TOOLS = new Set(["plan_create", "plan_get", "plan_revise"]);

export function PlanProposalCard({
  message,
  onQuickReply,
}: {
  message: ChatMessage;
  onQuickReply?: (text: string) => void;
}) {
  const call = message.toolCall;
  const result = message.toolResult;
  const proposal = parseProposal(typeof result?.output === "string" ? result.output : undefined);
  const [decided, setDecided] = useState<"approved" | "discarded" | null>(null);
  const [inflight, setInflight] = useState<null | "approve" | "discard">(null);
  const [open, setOpen] = useState(true);

  // Reload truth: if the plan was approved / discarded elsewhere (work board,
  // another device, Jarvis via plan_approve), the card reflects it on mount.
  useEffect(() => {
    if (!proposal?.id) return;
    let cancelled = false;
    void fetchPlan(proposal.id).then((p) => {
      if (cancelled || !p) return;
      if (p.status === "active" || p.status === "completed" || p.status === "paused") setDecided("approved");
      else if (p.status === "cancelled" || p.status === "failed") setDecided("discarded");
    });
    return () => {
      cancelled = true;
    };
  }, [proposal?.id]);

  if (!call || !PLAN_PROPOSAL_TOOLS.has(call.name)) return null;
  // Not a proposal (a real work order created an active plan, or still
  // running): the generic card is the right surface.
  if (!proposal) return <ToolCallCard message={message} />;

  const approve = async () => {
    setInflight("approve");
    const ok = await approvePlan(proposal.id);
    setInflight(null);
    if (!ok) {
      toast.error("Couldn't approve the plan. Try again.");
      return;
    }
    setDecided("approved");
    onQuickReply?.("Go ahead with the plan.");
  };
  const discard = async () => {
    setInflight("discard");
    const ok = await discardPlan(proposal.id);
    setInflight(null);
    if (!ok) {
      toast.error("Couldn't set the plan aside. Try again.");
      return;
    }
    setDecided("discarded");
    onQuickReply?.("Not yet. Let's keep talking it through before you build anything.");
  };

  const stepCount = proposal.steps.length;

  return (
    <div
      className={cn(
        "min-w-0 max-w-full overflow-hidden rounded-2xl border bg-card text-card-foreground shadow-sm",
        decided === "approved" ? "border-success/40" : decided === "discarded" ? "border-border/60 opacity-80" : "border-info/40",
      )}
    >
      <div className="flex items-start gap-3 px-4 pt-4">
        <div
          className={cn(
            "mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl",
            decided === "approved" ? "bg-success/15 text-success" : "bg-info/15 text-info",
          )}
        >
          <ClipboardList className="size-4" aria-hidden />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {decided === "approved" ? "Plan approved" : decided === "discarded" ? "Plan set aside" : "Proposed plan · waiting for your go"}
          </div>
          <div className="mt-0.5 break-words text-sm font-semibold leading-snug">{proposal.title}</div>
          {proposal.goal && (
            <p className="mt-1 break-words text-xs leading-relaxed text-muted-foreground">{proposal.goal}</p>
          )}
        </div>
      </div>

      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="mt-3 flex min-h-11 w-full items-center gap-2 px-4 text-left text-xs text-muted-foreground"
        aria-expanded={open}
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        <span>
          {stepCount} step{stepCount === 1 ? "" : "s"}
        </span>
      </button>

      {open && (
        <ol className="space-y-2 px-4 pb-2">
          {proposal.steps.map((s, i) => (
            <li key={s.id ?? i} className="flex min-w-0 gap-3">
              <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold tabular-nums text-muted-foreground">
                {i + 1}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="break-words text-sm leading-snug">{s.title}</span>
                  {s.is_checkpoint && (
                    <Badge variant="outline" className="gap-1 text-[10px]">
                      <Flag className="size-3" /> checks with you
                    </Badge>
                  )}
                  {s.verify_required && (
                    <Badge variant="outline" className="gap-1 text-[10px]">
                      <ShieldCheck className="size-3" /> verified
                    </Badge>
                  )}
                </div>
                {s.detail && <p className="mt-0.5 break-words text-xs text-muted-foreground">{s.detail}</p>}
              </div>
            </li>
          ))}
        </ol>
      )}

      <div className="flex flex-col gap-2 border-t border-border/60 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-xs text-muted-foreground">
          {decided === "approved"
            ? "Jarvis is on it. The plan now shows above the composer as he works."
            : decided === "discarded"
              ? "Nothing was built. Keep talking; he'll propose again when it takes shape."
              : "Nothing runs until you say so."}
        </p>
        {!decided && (
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              className="flex-1 sm:flex-none"
              disabled={inflight !== null}
              onClick={() => void discard()}
            >
              {inflight === "discard" ? <Spinner className="size-4" /> : <X className="size-4" />}
              Not yet
            </Button>
            <Button size="sm" className="flex-1 sm:flex-none" disabled={inflight !== null} onClick={() => void approve()}>
              {inflight === "approve" ? <Spinner className="size-4" /> : <Check className="size-4" />}
              Go ahead
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
