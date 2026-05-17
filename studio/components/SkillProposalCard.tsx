"use client";

import Link from "next/link";
import { useState } from "react";
import { Sparkles, ExternalLink, Check, X, Pencil, Loader2 } from "lucide-react";
import type { ChatMessage } from "@/hooks/useChat";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { ModalCode } from "@/components/ui/modal-content";
import { decideSkillProposal } from "@/lib/api";
import { toast } from "@/components/ui/sonner";

/**
 * SkillProposalCard - specialized tool-call surface for skill_propose and
 * skill_optimize results. Renders inline in /live so the boss sees
 * "new skill proposed" without leaving chat. Three actions live inline:
 * Approve & install (POST /api/voyager/proposals/:id/decide with
 * decision="promoted"), Review body (opens ResponsiveModal with the
 * SKILL.md), and Dismiss (decision="rejected").
 *
 * Shape of the result payload (set by tools/skill_tools.go):
 *
 *   { status: "proposed", id, name?, parent_skill?, description?,
 *     reasoning?, risk_level?, body?, message }
 */
export function SkillProposalCard({ message }: { message: ChatMessage }) {
  const call = message.toolCall;
  const result = message.toolResult;
  const [decided, setDecided] = useState<"installed" | "dismissed" | null>(null);
  const [inflight, setInflight] = useState<null | "approve" | "dismiss">(null);
  const [showBody, setShowBody] = useState(false);

  if (!call) return null;
  if (call.name !== "skill_propose" && call.name !== "skill_optimize") return null;

  const parsed = safeParse(typeof result?.output === "string" ? result.output : "");
  const isUpdate = call.name === "skill_optimize";
  const proposalId = typeof parsed?.id === "string" ? parsed.id : "";
  const name =
    (typeof parsed?.name === "string" && parsed.name) ||
    (typeof parsed?.parent_skill === "string" && parsed.parent_skill) ||
    "untitled";
  const body =
    (typeof parsed?.body === "string" && parsed.body) ||
    (typeof parsed?.skill_md === "string" && parsed.skill_md) ||
    "";
  const risk = typeof parsed?.risk_level === "string" ? parsed.risk_level : "";

  // Status / wording
  let label: string;
  let detail: string;
  let phase: "loading" | "error" | "proposed" | "decided";
  if (!result) {
    phase = "loading";
    label = isUpdate ? "Drafting skill update" : "Drafting a skill";
    detail = "Jarvis is putting together a proposal from this turn.";
  } else if (decided === "installed") {
    phase = "decided";
    label = isUpdate ? `Update installed for ${name}` : `Installed: ${name}`;
    detail = "Active immediately on the next turn.";
  } else if (decided === "dismissed") {
    phase = "decided";
    label = isUpdate ? `Update dismissed for ${name}` : `Dismissed: ${name}`;
    detail = "Skill proposal rejected. Jarvis won't re-propose this exact one.";
  } else if (result.is_error || (parsed && parsed.status !== "proposed")) {
    phase = "error";
    label = isUpdate ? "Skill update failed" : "Skill proposal failed";
    detail =
      (parsed && typeof parsed.message === "string" && parsed.message) ||
      "The proposal didn't land. The agent may try again on a future turn.";
  } else {
    phase = "proposed";
    label = isUpdate ? `Update proposed for ${name}` : `New skill proposed: ${name}`;
    detail =
      (typeof parsed?.description === "string" && parsed.description) ||
      (typeof parsed?.reasoning === "string" && parsed.reasoning) ||
      "Review the body to install or dismiss.";
  }

  async function decide(decision: "promoted" | "rejected") {
    if (!proposalId) {
      toast.error("Missing proposal id; reload the page and try again.");
      return;
    }
    setInflight(decision === "promoted" ? "approve" : "dismiss");
    const ok = await decideSkillProposal(proposalId, decision);
    setInflight(null);
    if (!ok) {
      toast.error("Decision failed. Try again or open Skills tab.");
      return;
    }
    setDecided(decision === "promoted" ? "installed" : "dismissed");
    toast.success(
      decision === "promoted"
        ? `Skill ${name} installed`
        : `Skill ${name} dismissed`,
    );
  }

  const accentClass =
    phase === "error"
      ? "border-destructive/30 bg-destructive/5"
      : phase === "decided"
        ? "border-brand/40 bg-brand/5"
        : "border-brand/30 bg-brand/5";
  const iconClass =
    phase === "error"
      ? "bg-destructive/15 text-destructive"
      : "bg-brand/15 text-brand";

  return (
    <>
      <div className={`overflow-hidden rounded-xl border ${accentClass}`}>
        <div className="flex items-start gap-2 px-3 py-2.5">
          <div className={`mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full ${iconClass}`}>
            {phase === "loading" ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden />
            ) : (
              <Sparkles className="size-3.5" aria-hidden />
            )}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-1.5 text-sm font-semibold text-foreground">
              <span className="break-all">{label}</span>
              {risk && phase === "proposed" && (
                <Badge variant="secondary" className="text-[10px] uppercase">
                  {risk}
                </Badge>
              )}
            </div>
            <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
              {detail}
            </p>
            {phase === "proposed" && (
              <div className="mt-2 flex flex-wrap items-center gap-1.5">
                <Button
                  size="sm"
                  className="h-8 bg-brand text-brand-foreground hover:bg-brand/90"
                  onClick={() => decide("promoted")}
                  disabled={inflight !== null}
                >
                  {inflight === "approve" ? (
                    <Loader2 className="size-3.5 animate-spin" aria-hidden />
                  ) : (
                    <Check className="size-3.5" aria-hidden />
                  )}
                  Approve & install
                </Button>
                {body && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-8"
                    onClick={() => setShowBody(true)}
                    disabled={inflight !== null}
                  >
                    <Pencil className="size-3.5" aria-hidden />
                    Review body
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-8 text-muted-foreground hover:text-destructive"
                  onClick={() => decide("rejected")}
                  disabled={inflight !== null}
                >
                  {inflight === "dismiss" ? (
                    <Loader2 className="size-3.5 animate-spin" aria-hidden />
                  ) : (
                    <X className="size-3.5" aria-hidden />
                  )}
                  Dismiss
                </Button>
              </div>
            )}
            {phase === "decided" && decided === "installed" && (
              <Link
                href="/skills"
                className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-brand hover:underline"
              >
                Open in Skills
                <ExternalLink className="size-3" aria-hidden />
              </Link>
            )}
          </div>
        </div>
      </div>

      {body && (
        <ResponsiveModal
          open={showBody}
          onOpenChange={setShowBody}
          size="lg"
          title={name}
          description={
            isUpdate
              ? "Review the updated SKILL.md before installing. Approve to replace the current version."
              : "Review the proposed SKILL.md before installing."
          }
          header={
            <ResponsiveModalHeader
              icon={<Sparkles className="size-4" />}
              eyebrow={isUpdate ? "Skill update" : "New skill"}
              title={name}
              subtitle={
                isUpdate
                  ? "Review the updated SKILL.md before installing."
                  : "Review the proposed SKILL.md before installing."
              }
            />
          }
          footer={
            <>
              <Button variant="ghost" onClick={() => setShowBody(false)}>
                Close
              </Button>
              <Button
                className="bg-brand text-brand-foreground hover:bg-brand/90"
                onClick={async () => {
                  setShowBody(false);
                  await decide("promoted");
                }}
                disabled={inflight !== null}
              >
                <Check className="size-4" /> Approve & install
              </Button>
            </>
          }
        >
          <ModalCode>{body}</ModalCode>
        </ResponsiveModal>
      )}
    </>
  );
}

function safeParse(s: string): Record<string, unknown> | null {
  if (!s) return null;
  try {
    const v = JSON.parse(s);
    return typeof v === "object" && v !== null ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}
