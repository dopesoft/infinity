"use client";

import { ActivityStepFor } from "@/components/chat/ActivityStep";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * ToolCallCard — compatibility shim over the activity ledger (MAJORDOMO §6).
 *
 * The bordered card that used to render a tool call is gone: a tool call is a
 * LINE in the turn's ledger now, not a box in the transcript. Everything the
 * card did lives on in `components/chat/ActivityStep.tsx` — the awaiting-
 * approval flow and its `contract_id`, the `BLOCKED:` gated state and its
 * Trust link, the live code-write preview, the delegate wording, the frozen
 * timer on a stopped call — with the words themselves coming from the tested
 * vocabulary in `lib/chat/activity.ts` instead of a fallback to the raw tool
 * id, which is the bug that motivated the rewrite.
 *
 * The file stays because `PlanProposalCard` falls back to it when a plan tool
 * has no parseable proposal. That caller now gets a ledger row.
 */
export function ToolCallCard({ message }: { message: ChatMessage }) {
  return <ActivityStepFor message={message} />;
}
