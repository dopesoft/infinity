"use client";

import { AlertTriangle, FileCode, HelpCircle, Terminal } from "lucide-react";
import { Section, TileCard } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { Approval, ApprovalKind, DashboardItem } from "@/lib/dashboard/types";

/* Approvals — items where the agent is waiting on the boss.
 *
 * Three flavors land here:
 *  - trust_* — high-risk tool calls gated by ClaudeCodeGate
 *  - code_proposal — Voyager source extractor drafts
 *  - curiosity — questions Jarvis has for you
 *
 * Tapping a row opens the ObjectViewer with the full payload — bash args
 * for trust, diff for code, question + context for curiosity.
 */
export function ApprovalsCard({
  approvals,
  onOpen,
}: {
  approvals: Approval[];
  onOpen: (item: DashboardItem) => void;
}) {
  const emptyText = "No open questions from Jarvis.";

  return (
    <Section
      title="Questions"
      Icon={HelpCircle}
      delay={0.25}
      badge={approvals.length}
    >
      <div className="space-y-2">
        {approvals.length === 0 ? (
          <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
            {emptyText}
          </div>
        ) : (
          <ul className="space-y-2">
            {approvals.map((a) => (
              <li key={a.id}>
                <ApprovalRow a={a} onClick={() => onOpen({ kind: "approval", data: a })} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

const KIND_META: Record<
  ApprovalKind,
  { Icon: typeof Terminal; tone: "warning" | "info"; label: string }
> = {
  trust_bash: { Icon: Terminal, tone: "warning", label: "bash" },
  trust_edit: { Icon: FileCode, tone: "warning", label: "edit" },
  trust_write: { Icon: FileCode, tone: "warning", label: "write" },
  code_proposal: { Icon: FileCode, tone: "info", label: "code" },
  curiosity: { Icon: HelpCircle, tone: "info", label: "asks" },
};

function ApprovalRow({ a, onClick }: { a: Approval; onClick: () => void }) {
  const meta = KIND_META[a.kind];
  return (
    <TileCard onClick={onClick} tone={meta.tone}>
      <span
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-md border",
          meta.tone === "warning"
            ? "border-rose-400/40 bg-rose-400/15 text-rose-400"
            : "border-info/40 bg-info/15 text-info",
        )}
      >
        <meta.Icon className="size-4" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span
            className={cn(
              "font-mono text-[10px] uppercase tracking-[0.14em]",
              meta.tone === "warning" ? "text-rose-400" : "text-info",
            )}
          >
            {meta.label}
          </span>
          <span
            className="shrink-0 whitespace-nowrap font-mono text-[10px] text-muted-foreground"
            suppressHydrationWarning
          >
            · {relTime(a.createdAt)}
          </span>
        </div>
        <p className="mt-0.5 truncate text-sm font-medium text-foreground">{a.title}</p>
        {a.subtitle ? (
          <p className="truncate text-[11px] text-muted-foreground">{a.subtitle}</p>
        ) : null}
      </div>
      {a.riskLevel === "high" || a.riskLevel === "critical" ? (
        <AlertTriangle className="size-3.5 shrink-0 text-danger" aria-hidden />
      ) : null}
    </TileCard>
  );
}
