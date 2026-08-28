"use client";

import { ListRow } from "@/components/ui/list-row";
import { relTime } from "@/lib/dashboard/format";
import type { Approval, ApprovalKind } from "@/lib/dashboard/types";

/* ApprovalRow - a single agent-raised approval/question row.
 *
 * Three flavors land here:
 *  - trust_* - high-risk tool calls gated by ClaudeCodeGate
 *  - code_proposal - Voyager source extractor drafts
 *  - curiosity - questions Jarvis has for you
 *
 * These no longer get their own "Questions" card - they're merged into
 * the unified "Surfaced by Jarvis" card (SurfacedCard), which renders this
 * row for every `kind:"approval"` item. Tapping a row opens the
 * ObjectViewer with the full payload.
 *
 * MAJORDOMO SWEEP: was a `TileCard` carrying a `size-9 rounded-md border`
 * tinted icon tile, a coloured mono kind label, and a trailing AlertTriangle.
 * It is now a `ListRow`: the tone DOT carries the signal (warning = waiting on
 * the boss, danger = high/critical risk, info = an FYI-grade proposal or
 * question) and the kind label + timestamp + subtitle fold into the one quiet
 * meta line. Same data, same tap target, no chrome.
 */

const KIND_LABEL: Record<ApprovalKind, string> = {
  trust_bash: "bash",
  trust_edit: "edit",
  trust_write: "write",
  code_proposal: "code",
  curiosity: "asks",
};

/** Waiting-on-you flavours read amber; a proposal or a question reads info. */
const KIND_TONE: Record<ApprovalKind, "warning" | "info"> = {
  trust_bash: "warning",
  trust_edit: "warning",
  trust_write: "warning",
  code_proposal: "info",
  curiosity: "info",
};

export function ApprovalRow({ a, onClick }: { a: Approval; onClick: () => void }) {
  const risky = a.riskLevel === "high" || a.riskLevel === "critical";
  const tone = risky ? "danger" : KIND_TONE[a.kind];
  const meta = [KIND_LABEL[a.kind], relTime(a.createdAt), a.subtitle]
    .filter(Boolean)
    .join(" · ");

  return (
    <ListRow
      tone={tone}
      title={a.title}
      meta={<span suppressHydrationWarning>{meta}</span>}
      onClick={onClick}
    />
  );
}
