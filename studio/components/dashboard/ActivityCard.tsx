"use client";

import { useMemo } from "react";
import { motion } from "framer-motion";
import {
  Activity,
  AlertTriangle,
  Brain,
  CheckCircle2,
  Clock4,
  Sparkles,
  type LucideIcon,
} from "lucide-react";
import { Section } from "./Section";
import { cn } from "@/lib/utils";
import { clockTime, relTime } from "@/lib/dashboard/format";
import type { ActivityEvent, ActivityKind, DashboardItem } from "@/lib/dashboard/types";

/* Activity feed - rolling stream of agent events.
 *
 * Time-ordered, with a "now" divider separating future (scheduled) from
 * past (completed). Each row taps into the ObjectViewer for the source
 * artifact. Visual style borrowed from /heartbeat (timeline with colored
 * dots) but flatter since this lives inside a card.
 */

const KIND_META: Record<
  ActivityKind,
  { Icon: LucideIcon; tone: "info" | "success" | "warning" | "danger" | "muted" | "procedural" }
> = {
  scheduled: { Icon: Clock4, tone: "muted" },
  completed: { Icon: CheckCircle2, tone: "success" },
  alert: { Icon: AlertTriangle, tone: "warning" },
  memory: { Icon: Brain, tone: "info" },
  reflection: { Icon: Sparkles, tone: "procedural" },
};

const DOT_CLS = {
  info: "bg-info shadow-[0_0_6px_hsl(var(--info))]",
  success: "bg-success shadow-[0_0_6px_hsl(var(--success))]",
  warning: "bg-rose-400 shadow-[0_0_6px_theme(colors.rose.400)]",
  danger: "bg-danger shadow-[0_0_6px_hsl(var(--danger))]",
  muted: "bg-muted-foreground/40",
  procedural: "bg-tier-procedural shadow-[0_0_6px_hsl(var(--tier-procedural))]",
} as const;

const ICON_CLS = {
  info: "text-info",
  success: "text-success",
  warning: "text-rose-400",
  danger: "text-danger",
  muted: "text-muted-foreground",
  procedural: "text-tier-procedural",
} as const;

export function ActivityCard({
  activity,
  onOpen,
}: {
  activity: ActivityEvent[];
  onOpen: (item: DashboardItem) => void;
}) {
  const sorted = useMemo(
    () =>
      [...activity].sort((a, b) => {
        // Future events sorted ascending (soonest first), past descending
        // (most-recent first). The divider rendered between groups.
        const ta = new Date(a.at).getTime();
        const tb = new Date(b.at).getTime();
        if (a.future && b.future) return ta - tb;
        if (a.future && !b.future) return -1;
        if (!a.future && b.future) return 1;
        return tb - ta;
      }),
    [activity],
  );
  const firstPast = sorted.findIndex((e) => !e.future);

  return (
    <Section title="Activity" Icon={Activity} delay={0.45} action={{ label: "open heartbeat", href: "/heartbeat" }}>
      <div className="overflow-hidden rounded-xl border bg-card">
        <ol className="relative max-h-[420px] overflow-y-auto px-4 py-3 scroll-touch">
          <span
            aria-hidden
            className="absolute bottom-3 left-[22px] top-3 w-px bg-border"
          />
          {sorted.map((e, i) => (
            <Row
              key={e.id}
              e={e}
              showDivider={i === firstPast && i !== 0}
              onClick={() => onOpen({ kind: "activity", data: e })}
            />
          ))}
        </ol>
      </div>
    </Section>
  );
}

function Row({
  e,
  showDivider,
  onClick,
}: {
  e: ActivityEvent;
  showDivider: boolean;
  onClick: () => void;
}) {
  const meta = KIND_META[e.kind];
  return (
    <>
      {showDivider ? (
        <li className="relative my-2 flex items-center gap-2 pl-7">
          <span className="font-mono text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
            now
          </span>
          <span className="h-px flex-1 bg-border" aria-hidden />
        </li>
      ) : null}
      <li className="relative">
        <motion.button
          type="button"
          onClick={onClick}
          whileHover={{ x: 2 }}
          transition={{ duration: 0.12 }}
          className="flex w-full items-start gap-3 rounded-md py-2 pl-7 pr-2 text-left transition-colors hover:bg-accent/40"
        >
          <span
            className={cn(
              "absolute left-[18px] top-3.5 size-2 rounded-full ring-4 ring-card",
              DOT_CLS[meta.tone],
            )}
            aria-hidden
          />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-baseline gap-2">
              <span
                // shrink-0 + whitespace-nowrap so "2m ago" / "5h ago"
                // never fold onto two lines when the title is long.
                // On mobile the activity column is narrow and without
                // these the timestamp was wrapping ("2m\nago") which
                // looked broken.
                className="shrink-0 whitespace-nowrap font-mono text-[10px] text-muted-foreground"
                suppressHydrationWarning
              >
                {e.future ? clockTime(e.at) : relTime(e.at)}
              </span>
              <span className="min-w-0 truncate text-[13px] font-medium text-foreground">
                {e.title}
              </span>
            </div>
            {e.detail ? (
              <p className="mt-0.5 line-clamp-1 break-words text-[11px] text-muted-foreground">
                {e.detail}
              </p>
            ) : null}
          </div>
          <meta.Icon
            className={cn("size-3.5 shrink-0", ICON_CLS[meta.tone])}
            aria-hidden
          />
        </motion.button>
      </li>
    </>
  );
}
