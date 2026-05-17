"use client";

import Link from "next/link";
import { Sparkles, ExternalLink } from "lucide-react";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * SkillProposalCard - specialized tool-call surface for skill_propose and
 * skill_optimize results. Renders inline in the conversation stream so the
 * boss immediately sees "oh shit, new skill" without having to scan the
 * generic tool-call card or check the Skills tab manually.
 *
 * Shape of the result payload (set by tools/skill_tools.go):
 *
 *   { status: "proposed", id, name?, parent_skill?, description?,
 *     reasoning?, risk_level?, message }
 *
 * Returns null when the tool call isn't a skill pipeline event.
 */
export function SkillProposalCard({ message }: { message: ChatMessage }) {
  const call = message.toolCall;
  const result = message.toolResult;
  if (!call) return null;
  if (call.name !== "skill_propose" && call.name !== "skill_optimize") return null;

  const parsed = safeParse(typeof result?.output === "string" ? result.output : "");
  const isUpdate = call.name === "skill_optimize";

  // Status / wording -
  //   running  : agent invoked but hasn't returned yet
  //   error    : non-zero exit / parse failure
  //   proposed : success path; show name + review link
  let label: string;
  let detail: string;
  if (!result) {
    label = isUpdate ? "Drafting skill update…" : "Drafting a skill…";
    detail = "Infinity is putting together a proposal from this turn.";
  } else if (result.is_error || (parsed && parsed.status !== "proposed")) {
    label = isUpdate ? "Skill update failed" : "Skill proposal failed";
    detail =
      (parsed && typeof parsed.message === "string" && parsed.message) ||
      "The proposal didn't land. The agent may try again on a future turn.";
  } else {
    const name = (parsed?.name as string) || (parsed?.parent_skill as string) || "untitled";
    label = isUpdate ? `Update proposed for ${name}` : `New skill proposed · ${name}`;
    detail =
      (typeof parsed?.description === "string" && parsed.description) ||
      (typeof parsed?.reasoning === "string" && parsed.reasoning) ||
      "Review in the Skills tab to promote or reject.";
  }

  return (
    <div className="overflow-hidden rounded-xl border border-info/30 bg-info/5">
      <div className="flex items-start gap-2 px-3 py-2.5">
        <div className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-info/15 text-info">
          <Sparkles className="size-3.5" aria-hidden />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
            <span className="truncate">{label}</span>
          </div>
          <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{detail}</p>
        </div>
        {result && !result.is_error && parsed?.status === "proposed" && (
          <Link
            href="/skills"
            className="inline-flex shrink-0 items-center gap-1 rounded-md border border-info/40 bg-info/10 px-2 py-1 text-[11px] font-medium text-info transition-colors hover:bg-info/15"
          >
            Review
            <ExternalLink className="size-3" aria-hidden />
          </Link>
        )}
      </div>
    </div>
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
