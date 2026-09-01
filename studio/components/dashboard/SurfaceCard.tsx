"use client";

import { ListRow } from "@/components/ui/list-row";
import { RunIndicator } from "@/lib/runs/RunIndicator";
import { SurfaceActionButton } from "./SurfaceActions";
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
 *
 * MAJORDOMO SWEEP: the importance-tinted `size-9 rounded-md border` icon tile,
 * the bell/quote glyph, and the trailing AlertTriangle are gone. Importance is
 * now the row's tone DOT (danger ≥ 80, info ≥ 50, resting grey below) — the
 * one alive signal, §1.4 — and WHAT it is + WHY it was raised + WHEN read as
 * title and one quiet meta line. The action buttons and their server-tracked
 * RunIndicator keep their place, now inside the row's own rule rather than
 * floating under it on a hand-measured indent.
 */
export function SurfaceRow({
  item,
  onClick,
}: {
  item: SurfaceItem;
  onClick: () => void;
}) {
  const imp = typeof item.importance === "number" ? item.importance : null;
  const tone = imp != null && imp >= 80 ? "danger" : imp != null && imp >= 50 ? "info" : "default";

  // The "why": prefer the assistant-written reason, then an explicit
  // subtitle, then a labelled body field. Never the raw multi-line body.
  const parsed = parseLabeledBody(item.body);
  const why =
    item.importanceReason?.trim() ||
    item.subtitle?.trim() ||
    parsed.find((f) => f.label.toLowerCase() === "why it matters")?.value ||
    parsed[0]?.value ||
    "";

  // A message somebody left him on the phone is MAIL: name the caller in the
  // meta so the row still says who it was from without a bespoke glyph.
  const from =
    item.surface === "messages" && typeof item.metadata?.from === "string"
      ? item.metadata.from
      : "";

  // Somebody tried to hide instructions in this message. Stamped on the row at
  // capture time, in his English, already stripped from what Jarvis reads. It
  // gets its own colour rather than joining the meta line because he should
  // learn that someone tried, not scan for it among the timestamps.
  const hiddenNotice =
    typeof item.metadata?.hidden_content_notice === "string"
      ? item.metadata.hidden_content_notice.trim()
      : "";

  const actions = item.actions ?? [];
  const meta = [from, why, relTime(item.updatedAt ?? item.createdAt)]
    .filter(Boolean)
    .join(" · ");

  return (
    <ListRow
      tone={tone}
      title={item.title}
      meta={
        <span suppressHydrationWarning>
          {meta}
          {hiddenNotice ? (
            <span className="block text-warning">{hiddenNotice}</span>
          ) : null}
        </span>
      }
      onClick={onClick}
    >
      {actions.length > 0 ? (
        <div className="flex flex-wrap items-center gap-2 pl-[30px]">
          {actions.map((a) => (
            <SurfaceActionButton key={a.id} itemId={item.id} action={a} />
          ))}
          {/* Server-tracked progress: survives navigation/refresh/device. */}
          <RunIndicator kind="surface.action" targetId={item.id} mode="inline" />
        </div>
      ) : null}
    </ListRow>
  );
}
