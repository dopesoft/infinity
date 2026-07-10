"use client";

import { AlertTriangle, Bell } from "lucide-react";
import { TileCard } from "./Section";
import { RunIndicator } from "@/lib/runs/RunIndicator";
import { SurfaceActionButton } from "./SurfaceActions";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import { parseLabeledBody } from "@/lib/dashboard/parseBody";
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
  // A surfaced item is something Jarvis raised - it has no "sender". The two
  // lines that matter are WHAT it is (title) and WHY it was raised
  // (importanceReason). Same icon-tile grammar as the other dashboard rows:
  //   leading tile (tinted by importance) · title + time · the "why" subtext
  // Importance shows through the tile tint + a trailing alert glyph for the
  // top tier - never a colored left-edge bar. Tapping opens ObjectViewer.
  const imp = typeof item.importance === "number" ? item.importance : null;
  const high = imp != null && imp >= 80;
  const mid = imp != null && imp >= 50 && imp < 80;
  const tile = high
    ? "border-rose-400/40 bg-rose-400/15 text-rose-400"
    : mid
      ? "border-info/40 bg-info/15 text-info"
      : "border-border bg-muted text-muted-foreground";

  // The "why": prefer the assistant-written reason, then an explicit
  // subtitle, then a labelled body field. Never the raw multi-line body.
  const parsed = parseLabeledBody(item.body);
  const why =
    item.importanceReason?.trim() ||
    item.subtitle?.trim() ||
    parsed.find((f) => f.label.toLowerCase() === "why it matters")?.value ||
    parsed[0]?.value ||
    "";

  const actions = item.actions ?? [];

  return (
    <div className="flex min-w-0 flex-col">
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
              {relTime(item.updatedAt ?? item.createdAt)}
            </span>
          </div>
          {why ? (
            <p className="mt-0.5 line-clamp-1 break-words text-[12px] text-muted-foreground">
              {why}
            </p>
          ) : null}
        </div>
        {high ? (
          <AlertTriangle className="size-3.5 shrink-0 text-danger" aria-hidden />
        ) : null}
      </TileCard>
      {actions.length > 0 ? (
        <div className="mt-1.5 flex flex-wrap items-center gap-2 pl-12">
          {actions.map((a) => (
            <SurfaceActionButton key={a.id} itemId={item.id} action={a} />
          ))}
          {/* Server-tracked progress: survives navigation/refresh/device. */}
          <RunIndicator kind="surface.action" targetId={item.id} mode="inline" />
        </div>
      ) : null}
    </div>
  );
}
