"use client";

import * as React from "react";
import {
  AlertTriangle,
  Check,
  Circle,
  Minus,
  X,
} from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { RunIndicator } from "@/lib/runs/RunIndicator";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { PlanStep, PlanStepStatus } from "@/lib/dashboard/types";

/* PlanTimeline - the step list for a durable plan ("the Cortex").
 *
 * Shared so the one list renders wherever a plan is shown - currently inside
 * ObjectViewer when a "plan" Agent Work item is opened.
 *
 * Majordomo phase 3 restyled it to the ledger shape from the mockup: a bare
 * status glyph (check for done, the one brand spinner for running, a faint
 * ring for pending), the step title, and quiet meta under it. What is GONE is
 * the chrome: the bordered status circles, the bordered checkpoint/verify
 * chips, the bordered evidence box, and the connector rail - three levels of
 * box inside a modal that is already a box (§1.2, §2).
 *
 * Everything it CARRIES is untouched: checkpoint and verify state, the verify
 * verdict and its evidence, the live `RunIndicator` for an executing step
 * (which survives navigation), the result summary and the ended-at stamp.
 *
 * Phase 2b resolved the open question here: this row stays, and it does NOT
 * become <ActivityStep>. They share an anatomy on purpose (glyph, title, quiet
 * meta, detail) and they must keep sharing it, but they are two different
 * things underneath. ActivityStep renders an `ActivityItem` - a coalesced run
 * of tool calls off the WebSocket, whose words come from the tested vocabulary
 * in lib/chat/activity.ts and whose one interaction is open/close. A PlanStep
 * is a durable `mem_plans` row with a verify verdict, evidence, a checkpoint
 * flag, a result summary and a live RunIndicator keyed by run id. Feeding one
 * into the other means either dropping half of that or teaching ActivityStep a
 * second data model and five more content slots - which is precisely the
 * "general-purpose step component" the phase-3 note warned against. Keep the
 * shape identical by copying the classes; keep the components separate. */
export function PlanTimeline({ steps }: { steps: PlanStep[] }) {
  if (!steps.length) {
    return <p className="text-[13px] text-quiet">No steps yet.</p>;
  }
  return (
    <ol className="min-w-0 space-y-2.5">
      {steps.map((step) => (
        <StepRow key={step.id} step={step} />
      ))}
    </ol>
  );
}

function StepRow({ step }: { step: PlanStep }) {
  const verdict =
    typeof step.verifyResult?.verdict === "string"
      ? (step.verifyResult.verdict as string)
      : undefined;
  const evidence =
    typeof step.verifyResult?.evidence === "string"
      ? (step.verifyResult.evidence as string)
      : undefined;
  const running = step.status === "in_progress";

  // Checkpoint / verify state as words in the quiet meta line, not as pills.
  const tags: string[] = [];
  if (step.isCheckpoint) tags.push("checkpoint");
  if (step.verifyRequired) {
    tags.push(
      verdict === "pass" ? "verified" : verdict === "fail" ? "failed verify" : "needs verify",
    );
  }

  return (
    <li className="flex min-w-0 gap-2.5">
      <StepGlyph status={step.status} />

      <div className="min-w-0 flex-1">
        <p
          className={cn(
            "min-w-0 break-words text-[13.5px] font-medium leading-snug",
            step.status === "done" || step.status === "skipped"
              ? "text-quiet"
              : "text-foreground",
          )}
        >
          {step.title}
        </p>

        {tags.length > 0 ? (
          <p
            className={cn(
              "mt-0.5 text-[11.5px] leading-snug",
              verdict === "fail" ? "text-danger" : "text-quiet",
            )}
          >
            {tags.join(" · ")}
          </p>
        ) : null}

        {step.detail?.trim() ? (
          <p className="mt-0.5 break-words text-[12px] leading-relaxed text-quiet">
            {step.detail.trim()}
          </p>
        ) : null}

        {step.resultSummary?.trim() ? (
          <p className="mt-0.5 break-words text-[12px] leading-relaxed text-foreground/75">
            {step.resultSummary.trim()}
          </p>
        ) : null}

        {evidence ? (
          <p
            className={cn(
              "mt-1 break-words text-[11.5px] leading-relaxed",
              verdict === "fail" ? "text-danger" : "text-quiet",
            )}
          >
            <span className="font-mono uppercase tracking-[0.14em] opacity-70">evidence </span>
            {evidence}
          </p>
        ) : null}

        {/* Live spinner for an executing step (survives navigation). */}
        {running && step.runId ? (
          <div className="mt-1">
            <RunIndicator kind="plan.step" targetId={step.id} mode="inline" />
          </div>
        ) : null}

        {step.endedAt ? (
          <p
            className="mt-0.5 text-[11.5px] leading-snug text-quiet"
            suppressHydrationWarning
          >
            {step.status} {relTime(step.endedAt)}
          </p>
        ) : null}
      </div>
    </li>
  );
}

/* StepGlyph - the status mark in the gutter. A bare 14px glyph, no circle, no
 * border, no tinted tile. Emerald marks the ONE thing happening right now
 * (§1.4) and is the only spinner in the list; everything settled is grey. */
function StepGlyph({ status }: { status: PlanStepStatus }) {
  const base = "mt-px size-[15px] shrink-0";
  switch (status) {
    case "done":
      return <Check className={cn(base, "text-quiet")} strokeWidth={2.5} aria-label="done" />;
    case "in_progress":
      return (
        <Spinner className={cn(base, "text-brand")} aria-hidden={false} aria-label="running" />
      );
    case "blocked":
      return <AlertTriangle className={cn(base, "text-warning")} aria-label="blocked" />;
    case "failed":
      return <X className={cn(base, "text-danger")} strokeWidth={2.5} aria-label="failed" />;
    case "skipped":
      return <Minus className={cn(base, "text-quiet")} aria-label="skipped" />;
    default: // pending
      return <Circle className={cn(base, "text-quiet/50")} aria-label="pending" />;
  }
}
