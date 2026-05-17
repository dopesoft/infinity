"use client";

import { CircleDot } from "lucide-react";
import { Section, TileCard } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import { extractFromSender, parseLabeledBody } from "@/lib/dashboard/parseBody";
import type { DashboardItem, SurfaceItem } from "@/lib/dashboard/types";

/* SurfaceCard - the ONE generic renderer for the dashboard surface
 * contract (mem_surface_items). Every item the agent surfaces via the
 * `surface_item` tool lands here, grouped by `surface` key. There is no
 * per-source widget: a triage skill, a connector poll, a cron, or the
 * agent itself can invent a new `surface` and it renders with zero new
 * frontend code. Tap a row → ObjectViewer with the full item.
 *
 * This is the Rule #1 payoff on the Studio side - the app adapts to
 * whatever the agent assembles.
 */

// A few well-known surfaces get a friendlier title + action link. The map
// is a nicety, not a gate - an unknown surface still renders, with a
// titleized key and no action.
const SURFACE_META: Record<
  string,
  { title: string; action?: { label: string; href: string } }
> = {
  followups: { title: "Follow-ups", action: { label: "see inbox", href: "/memory" } },
  // Jarvis's OWN goals (mem_agent_goals) - what the agent is working toward
  // for the boss. Distinct from the boss's Pursuits card (mem_pursuits).
  agenda: { title: "Jarvis's agenda" },
  health: { title: "Needs attention" },
  approvals: { title: "Approvals" },
  alerts: { title: "Alerts" },
  digest: { title: "Digest" },
  insights: { title: "Insights" },
  briefing: { title: "Briefing" },
};

function titleize(key: string): string {
  return key.replace(/[-_]+/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export function SurfaceCard({
  surface,
  items,
  delay = 0,
  onOpen,
}: {
  surface: string;
  items: SurfaceItem[];
  delay?: number;
  onOpen: (item: DashboardItem) => void;
}) {
  const meta = SURFACE_META[surface];
  return (
    <Section
      title={meta?.title ?? titleize(surface)}
      Icon={CircleDot}
      delay={delay}
      badge={items.length}
      action={meta?.action}
    >
      <div className="space-y-2">
        {items.length === 0 ? (
          <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
            Nothing surfaced here yet.
          </div>
        ) : (
          <ul className="space-y-1.5">
            {items.map((it) => (
              <li key={it.id}>
                <SurfaceRow
                  item={it}
                  onClick={() => onOpen({ kind: "surface", data: it })}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

function SurfaceRow({ item, onClick }: { item: SurfaceItem; onClick: () => void }) {
  // Untitled UI list-row pattern:
  //   line 1: sender (parsed from body's "From:" field) · time on the right
  //   line 2: subject (item.title) — the heaviest type weight
  //   line 3: assistant-written one-liner (importanceReason) — the "why"
  // Importance is a discrete signal only: a 2px colored left edge when
  // imp >= 50. No avatar bubble, no IMPORTANT/NOTABLE pill, no chips,
  // no star, no 3-dot menu, no tags. Tapping the row opens ObjectViewer
  // (the only action). All "act on it" is funnelled through Discuss
  // with Jarvis inside the modal.
  const imp = typeof item.importance === "number" ? item.importance : null;
  const edge =
    imp != null && imp >= 80
      ? "border-l-2 border-l-danger"
      : imp != null && imp >= 50
        ? "border-l-2 border-l-info"
        : null;

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

  return (
    <TileCard
      onClick={onClick}
      className={cn("flex-col items-stretch gap-1.5 p-4 sm:p-4", edge)}
    >
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-foreground">
          {sender}
        </span>
        <span
          className="shrink-0 font-mono text-[10px] uppercase tracking-wider text-muted-foreground"
          suppressHydrationWarning
        >
          {relTime(item.createdAt)}
        </span>
      </div>
      <p className="line-clamp-2 break-words text-[14px] font-semibold leading-snug text-foreground">
        {item.title}
      </p>
      {preview ? (
        <p className="line-clamp-1 break-words text-[12.5px] text-muted-foreground">
          {preview}
        </p>
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
