import * as React from "react";
import { cn } from "@/lib/utils";

/* Chip - the shared dashboard tag primitive.
 *
 * Rectangular tag with slightly rounded corners (rounded-md) and a thin
 * border so each chip reads as a distinct object instead of flowing into
 * the row like prose. Tone tints bg/text/border for known signals; stays
 * neutral for free-form strings.
 *
 * Lives here (not inline in a single card) per the reuse-first rule: the
 * Follow-ups card AND the ObjectViewer render the same intent/mode/
 * classification chips, so the styling + tone mapping has ONE home. Extend
 * here (new tone, new size) and every consumer benefits. */

export type ChipTone = "muted" | "info" | "warn" | "success" | "danger" | "brand";

const TONE_CLASSES: Record<ChipTone, string> = {
  muted: "border-border bg-muted text-muted-foreground",
  info: "border-info/30 bg-info/10 text-info",
  warn: "border-amber-500/30 bg-amber-500/10 text-amber-400",
  success: "border-brand/30 bg-brand/10 text-brand",
  danger: "border-danger/30 bg-danger/10 text-danger",
  brand: "border-brand/30 bg-brand/10 text-brand",
};

export function Chip({
  children,
  tone = "muted",
  icon,
  className,
}: {
  children: React.ReactNode;
  tone?: ChipTone;
  /** Optional leading icon (Lucide at size-3). */
  icon?: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1 rounded-md border px-1.5 font-mono text-[10px] uppercase tracking-wider",
        TONE_CLASSES[tone],
        className,
      )}
    >
      {icon ? <span className="shrink-0">{icon}</span> : null}
      {children}
    </span>
  );
}

// intent: "needs reply" | "fyi" | "urgent" | "question" | "scheduling" | "done"
export function intentTone(v: string): ChipTone {
  const k = v.toLowerCase();
  if (k.includes("urgent") || k.includes("critical")) return "danger";
  if (k.includes("needs") || k.includes("waiting") || k.includes("reply")) return "warn";
  if (k.includes("fyi") || k.includes("info")) return "info";
  if (k.includes("done") || k.includes("resolved")) return "success";
  return "muted";
}

// mode: "reply" | "read" | "skim" | "ignore" - the recommended action.
export function modeTone(v: string): ChipTone {
  const k = v.toLowerCase();
  if (k === "reply" || k.includes("reply")) return "warn";
  if (k === "read" || k.includes("read")) return "info";
  if (k.includes("skim") || k.includes("scan")) return "muted";
  if (k.includes("ignore") || k.includes("dismiss")) return "muted";
  return "muted";
}

// classification: the triager's category label. Personal / work tint
// info-blue (matters), newsletter / promotion / automated stay muted
// (low-priority), spam goes danger. New classes the agent might invent
// degrade to muted so the chip still renders.
export function classificationTone(v: string): ChipTone {
  const k = v.toLowerCase();
  if (k === "personal" || k === "work") return "info";
  if (k === "transaction" || k === "scheduling") return "info";
  if (k === "spam") return "danger";
  if (k === "urgent") return "danger";
  if (k === "newsletter" || k === "promotion" || k === "automated" || k === "notification") {
    return "muted";
  }
  return "muted";
}
