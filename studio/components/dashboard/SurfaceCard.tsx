"use client";

import {
  AlertTriangle,
  AtSign,
  Calendar,
  CheckSquare,
  CircleDot,
  FileText,
  MessageCircle,
  Sparkles,
  TrendingUp,
  type LucideIcon,
} from "lucide-react";
import { Section, TileCard } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, SurfaceItem } from "@/lib/dashboard/types";

/* SurfaceCard — the ONE generic renderer for the dashboard surface
 * contract (mem_surface_items). Every item the agent surfaces via the
 * `surface_item` tool lands here, grouped by `surface` key. There is no
 * per-source widget: a triage skill, a connector poll, a cron, or the
 * agent itself can invent a new `surface` and it renders with zero new
 * frontend code. Tap a row → ObjectViewer with the full item.
 *
 * This is the Rule #1 payoff on the Studio side — the app adapts to
 * whatever the agent assembles.
 */

// kind → icon. `kind` is free-form on the backend; anything unmapped
// falls back to a neutral dot. Add a mapping here only when a kind earns
// a clearer glyph — never block rendering on it.
const KIND_ICON: Record<string, LucideIcon> = {
  email: AtSign,
  message: MessageCircle,
  alert: AlertTriangle,
  article: FileText,
  metric: TrendingUp,
  event: Calendar,
  task: CheckSquare,
  finding: Sparkles,
};

// A few well-known surfaces get a friendlier title + action link. The map
// is a nicety, not a gate — an unknown surface still renders, with a
// titleized key and no action.
const SURFACE_META: Record<
  string,
  { title: string; action?: { label: string; href: string } }
> = {
  followups: { title: "Follow-ups", action: { label: "see inbox", href: "/memory" } },
  // Jarvis's OWN goals (mem_agent_goals) — what the agent is working toward
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
  const Icon = KIND_ICON[item.kind] ?? CircleDot;
  // 80+ → "Important" chip. 50-79 → "Notable" chip. Below that, or
  // unranked → no chip. Importance is optional — an unranked item just
  // renders without one.
  const imp = typeof item.importance === "number" ? item.importance : null;
  const chip =
    imp != null && imp >= 80
      ? { label: "Important", cls: "bg-danger/15 text-danger" }
      : imp != null && imp >= 50
        ? { label: "Notable", cls: "bg-info/15 text-info" }
        : null;
  return (
    <TileCard onClick={onClick}>
      <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground">
        <Icon className="size-4" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-foreground">{item.title}</span>
          {chip && (
            <span
              title={item.importanceReason || undefined}
              className={cn(
                "shrink-0 rounded-full px-1.5 py-px text-[10px] font-medium uppercase tracking-wide",
                chip.cls,
              )}
            >
              {chip.label}
            </span>
          )}
          {item.subtitle ? (
            <span className="hidden truncate text-xs text-muted-foreground sm:inline">
              · {item.subtitle}
            </span>
          ) : null}
          <span
            className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground"
            suppressHydrationWarning
          >
            {relTime(item.createdAt)}
          </span>
        </div>
        {item.body ? (
          <p className="line-clamp-1 break-words text-[12px] text-muted-foreground">
            {item.body}
          </p>
        ) : null}
      </div>
    </TileCard>
  );
}
