"use client";

import { AlertTriangle, Bell } from "lucide-react";
import { TileCard } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import { extractFromSender, parseLabeledBody } from "@/lib/dashboard/parseBody";
import type { SurfaceItem } from "@/lib/dashboard/types";

/* SurfaceRow - a single row of the generic surface contract
 * (mem_surface_items). Every item the agent surfaces via the `surface_item`
 * tool renders through this row, regardless of its `surface` key. There is
 * no per-source widget: a triage skill, a connector poll, a cron, or the
 * agent itself can invent a new `surface` and it renders with zero new
 * frontend code.
 *
 * Surfaces no longer get their own per-key card - they're merged into the
 * unified "Surfaced by Jarvis" card (SurfacedCard), which renders this row
 * for every `kind:"surface"` item. Tap a row → ObjectViewer with the full
 * item. This is the Rule #1 payoff on the Studio side - the app adapts to
 * whatever the agent assembles.
 */
export function SurfaceRow({
  item,
  onClick,
}: {
  item: SurfaceItem;
  onClick: () => void;
}) {
  // Same icon-tile grammar as FollowUpRow / ApprovalRow so the whole
  // "Surfaced by Jarvis" card reads as one calm, consistent list:
  //   leading tile (tinted by importance) · title + time · sender/why subtext
  // Importance is signalled by the tile tint plus a trailing alert glyph
  // for the top tier - NOT a colored left-edge bar. Tapping opens ObjectViewer.
  const imp = typeof item.importance === "number" ? item.importance : null;
  const high = imp != null && imp >= 80;
  const mid = imp != null && imp >= 50 && imp < 80;
  const tile = high
    ? "border-rose-400/40 bg-rose-400/15 text-rose-400"
    : mid
      ? "border-info/40 bg-info/15 text-info"
      : "border-border bg-muted text-muted-foreground";

  // Sender comes from parsing the body's "From:" line. If parse misses,
  // fall back to the source label ("gmail-triage" → "gmail") so the row
  // still has a stable lead. Never show the noisy pipe-subtitle.
  const parsed = parseLabeledBody(item.body);
  const sender =
    extractFromSender(parsed) ??
    (item.source ? humaniseSource(item.source) : item.kind || "item");

  // Preview line. Prefer the assistant-written "why" (importanceReason).
  // Fall back to the first labelled body field's value, then to nothing.
  // Never the raw multi-line body dump.
  const preview =
    item.importanceReason?.trim() ||
    parsed.find((f) => f.label.toLowerCase() === "why it matters")?.value ||
    parsed[0]?.value ||
    "";
  // One muted subtext line: sender, then the "why" when present. Keeps the
  // row to two lines so it stays slim like the rest of the dashboard rows.
  const subtext = preview ? `${sender} — ${preview}` : sender;

  return (
    <TileCard onClick={onClick} className="gap-3 p-3">
      <span
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-md border",
          tile,
        )}
      >
        <Bell className="size-4" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
            {item.title}
          </span>
          <span
            className="shrink-0 font-mono text-[10px] text-muted-foreground"
            suppressHydrationWarning
          >
            {relTime(item.createdAt)}
          </span>
        </div>
        <p className="mt-0.5 line-clamp-1 break-words text-[12px] text-muted-foreground">
          {subtext}
        </p>
      </div>
      {high ? (
        <AlertTriangle className="size-3.5 shrink-0 text-danger" aria-hidden />
      ) : null}
    </TileCard>
  );
}

// gmail-triage → "gmail". slack-triage → "slack". Falls back to the
// source verbatim when no hyphen split is meaningful.
function humaniseSource(s: string): string {
  const head = s.split(/[-_]/)[0];
  return head.length > 0 ? head : s;
}
