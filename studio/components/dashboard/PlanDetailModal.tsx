"use client";

import * as React from "react";
import {
  AlertTriangle,
  Check,
  CircleDot,
  Flag,
  ListChecks,
  Loader2,
  Minus,
  ShieldCheck,
  X,
} from "lucide-react";
import {
  ResponsiveModal,
  ResponsiveModalHeader,
} from "@/components/ui/responsive-modal";
import { ModalSection } from "@/components/ui/modal-content";
import { RunIndicator } from "@/lib/runs/RunIndicator";
import { Chip, type ChipTone } from "./Chip";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { Plan, PlanStep, PlanStepStatus, PlanStatus } from "@/lib/dashboard/types";

/* PlanDetailModal - the read view for a durable plan ("the Cortex").
 *
 * Built entirely on ResponsiveModal + ModalSection (the sanctioned modal
 * primitives) so it carries the mobile-overflow / safe-area discipline for
 * free. The body is a vertical step timeline: a numbered, status-colored
 * gutter with a connector line, the step title + detail, verification
 * evidence, and a live RunIndicator for any step currently executing.
 *
 * Writes (advancing steps, resolving checkpoints) happen through the agent's
 * plan_* tools and the checkpoint surface-action card on the dashboard, never
 * here - this stays a viewing surface, consistent with the dashboard IA. */
export function PlanDetailModal({
  plan,
  open,
  onClose,
}: {
  plan: Plan | null;
  open: boolean;
  onClose: () => void;
}) {
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={(o) => (!o ? onClose() : null)}
      size="lg"
      title={plan?.title ?? "Plan"}
      description={plan ? planSubtitle(plan) : undefined}
      header={
        plan ? (
          <ResponsiveModalHeader
            icon={<ListChecks className="size-4" aria-hidden />}
            title={plan.title}
            subtitle={planSubtitle(plan)}
            tone={planStatusTone(plan.status)}
            titleSize="lg"
            titleClamp={2}
          />
        ) : undefined
      }
    >
      {plan ? <PlanBody plan={plan} /> : null}
    </ResponsiveModal>
  );
}

function PlanBody({ plan }: { plan: Plan }) {
  return (
    <div className="min-w-0 max-w-full">
      {plan.goal?.trim() ? (
        <ModalSection label="Goal">
          <p className="text-foreground/90">{plan.goal.trim()}</p>
        </ModalSection>
      ) : null}

      <ModalSection label={`Steps · ${plan.doneCount}/${plan.totalCount}`}>
        <ol className="min-w-0">
          {plan.steps.map((step, i) => (
            <StepRow
              key={step.id}
              step={step}
              isLast={i === plan.steps.length - 1}
            />
          ))}
          {plan.steps.length === 0 ? (
            <li className="text-[13px] text-muted-foreground">No steps yet.</li>
          ) : null}
        </ol>
      </ModalSection>
    </div>
  );
}

function StepRow({ step, isLast }: { step: PlanStep; isLast: boolean }) {
  const verdict =
    typeof step.verifyResult?.verdict === "string"
      ? (step.verifyResult.verdict as string)
      : undefined;
  const evidence =
    typeof step.verifyResult?.evidence === "string"
      ? (step.verifyResult.evidence as string)
      : undefined;
  const running = step.status === "in_progress";

  return (
    <li className="flex min-w-0 gap-3">
      {/* Gutter: status node + connector line */}
      <div className="flex flex-col items-center">
        <StepNode status={step.status} />
        {!isLast ? <div className="w-px flex-1 bg-border" aria-hidden /> : null}
      </div>

      {/* Content */}
      <div className="min-w-0 flex-1 pb-4">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span
            className={cn(
              "min-w-0 text-[13px] font-medium",
              step.status === "done" || step.status === "skipped"
                ? "text-muted-foreground"
                : "text-foreground",
              step.status === "done" && "line-through decoration-muted-foreground/40",
            )}
          >
            {step.title}
          </span>
          {step.isCheckpoint ? (
            <Chip tone="info" icon={<Flag className="size-3" aria-hidden />}>
              checkpoint
            </Chip>
          ) : null}
          {step.verifyRequired ? (
            <Chip
              tone={verdict === "pass" ? "success" : verdict === "fail" ? "danger" : "muted"}
              icon={<ShieldCheck className="size-3" aria-hidden />}
            >
              {verdict === "pass" ? "verified" : verdict === "fail" ? "failed verify" : "verify"}
            </Chip>
          ) : null}
        </div>

        {step.detail?.trim() ? (
          <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">
            {step.detail.trim()}
          </p>
        ) : null}

        {step.resultSummary?.trim() ? (
          <p className="mt-1 text-[12px] leading-relaxed text-foreground/75">
            {step.resultSummary.trim()}
          </p>
        ) : null}

        {evidence ? (
          <p
            className={cn(
              "mt-1.5 rounded-md border px-2 py-1 text-[11px] leading-relaxed",
              verdict === "fail"
                ? "border-danger/30 bg-danger/5 text-danger"
                : "border-success/30 bg-success/5 text-success",
            )}
          >
            <span className="font-mono uppercase tracking-wider opacity-70">evidence </span>
            {evidence}
          </p>
        ) : null}

        {/* Live spinner for an executing step (survives navigation). */}
        {running && step.runId ? (
          <div className="mt-1.5">
            <RunIndicator kind="plan.step" targetId={step.id} mode="inline" />
          </div>
        ) : null}

        {step.endedAt ? (
          <p
            className="mt-1 font-mono text-[10px] uppercase tracking-wider text-muted-foreground/70"
            suppressHydrationWarning
          >
            {step.status} {relTime(step.endedAt)}
          </p>
        ) : null}
      </div>
    </li>
  );
}

// StepNode - the numbered/iconed status circle in the timeline gutter.
function StepNode({ status }: { status: PlanStepStatus }) {
  const base =
    "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border";
  switch (status) {
    case "done":
      return (
        <span className={cn(base, "border-success/40 bg-success/10 text-success")}>
          <Check className="size-3.5" strokeWidth={3} aria-hidden />
        </span>
      );
    case "in_progress":
      return (
        <span className={cn(base, "border-brand/50 bg-brand/10 text-brand")}>
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
        </span>
      );
    case "blocked":
      return (
        <span className={cn(base, "border-warning/50 bg-warning/10 text-warning")}>
          <AlertTriangle className="size-3.5" aria-hidden />
        </span>
      );
    case "failed":
      return (
        <span className={cn(base, "border-danger/50 bg-danger/10 text-danger")}>
          <X className="size-3.5" strokeWidth={3} aria-hidden />
        </span>
      );
    case "skipped":
      return (
        <span className={cn(base, "border-border bg-muted text-muted-foreground")}>
          <Minus className="size-3.5" aria-hidden />
        </span>
      );
    default: // pending
      return (
        <span className={cn(base, "border-border bg-background text-muted-foreground/50")}>
          <CircleDot className="size-3.5" aria-hidden />
        </span>
      );
  }
}

export function planStatusTone(status: PlanStatus): string {
  switch (status) {
    case "completed":
      return "border-success/40 bg-success/10 text-success";
    case "failed":
    case "cancelled":
      return "border-danger/40 bg-danger/10 text-danger";
    case "paused":
      return "border-warning/40 bg-warning/10 text-warning";
    default:
      return "border-brand/40 bg-brand/10 text-brand";
  }
}

export function planStatusChipTone(status: PlanStatus): ChipTone {
  switch (status) {
    case "completed":
      return "success";
    case "failed":
    case "cancelled":
      return "danger";
    case "paused":
      return "warn";
    default:
      return "brand";
  }
}

function planSubtitle(plan: Plan): string {
  const parts = [`${plan.doneCount}/${plan.totalCount} steps`, plan.status];
  parts.push(`updated ${relTime(plan.updatedAt)}`);
  return parts.join(" · ");
}
