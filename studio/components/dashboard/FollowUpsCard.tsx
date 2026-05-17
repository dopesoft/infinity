"use client";

import { AtSign, Hash, Inbox, Workflow, MessageCircle, type LucideIcon } from "lucide-react";
import { Section, TileCard } from "./Section";
import { ScrollList } from "./ScrollList";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, FollowUp, FollowUpSource } from "@/lib/dashboard/types";

/* Follow-ups - humans (or connector-surfaced systems) waiting on you.
 *
 * Reads a unified `followUps` array sourced from BOTH mem_followups
 * (connector polls) and mem_surface_items with surface='followups' /
 * 'inbox' / 'email' (agent-surfaced triage). Same card, same chips,
 * same dismiss path - Rule #1: one surface concept, one place to
 * render. A triage skill the agent invents drops its output here just
 * by calling surface_item with surface='followups'; no new card is
 * spawned.
 *
 * Row shape:
 *   line 1: bold sender + relative time on the right
 *   line 2: chips - [account] [intent] [mode] - replaces the noisy
 *           preview subtext. Each chip is data-driven from metadata,
 *           so a new chip the agent attaches shows up automatically
 *           if its key matches a known signal.
 *
 * ScrollList max={4} keeps the card a calm 4 rows tall; the rest
 * scrolls internally so the dashboard never towers when the inbox is
 * busy.
 */

const SOURCE_META: Record<
  FollowUpSource,
  { Icon: LucideIcon; tone: string; label: string }
> = {
  gmail: { Icon: AtSign, tone: "text-info", label: "email" },
  slack: { Icon: Hash, tone: "text-rose-400", label: "slack" },
  imessage: { Icon: MessageCircle, tone: "text-success", label: "imsg" },
  linear: { Icon: Workflow, tone: "text-muted-foreground", label: "linear" },
  other: { Icon: Inbox, tone: "text-muted-foreground", label: "other" },
};

function metaFor(source: string): { Icon: LucideIcon; tone: string; label: string } {
  if (source in SOURCE_META) return SOURCE_META[source as FollowUpSource];
  return SOURCE_META.other;
}

export function FollowUpsCard({
  followUps,
  onOpen,
}: {
  followUps: FollowUp[];
  onOpen: (item: DashboardItem) => void;
}) {
  return (
    <Section
      title="Follow-ups"
      Icon={Inbox}
      delay={0.3}
      badge={followUps.length}
      action={{ label: "see inbox", href: "/memory" }}
    >
      {followUps.length === 0 ? (
        <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
          Inbox zero - no one is waiting on you.
        </div>
      ) : (
        <ScrollList max={4}>
          <ul className="space-y-1.5">
            {followUps.map((f) => (
              <li key={f.id}>
                <FollowUpRow f={f} onClick={() => onOpen({ kind: "followup", data: f })} />
              </li>
            ))}
          </ul>
        </ScrollList>
      )}
    </Section>
  );
}

// Pull a string-valued chip from the row's metadata. The agent attaches
// {intent, mode, classification, ...} when it triages; this is the
// reader. Returns "" when the key is missing or not a string.
function metaString(m: Record<string, unknown> | undefined, ...keys: string[]): string {
  if (!m) return "";
  for (const k of keys) {
    const v = m[k];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

function FollowUpRow({ f, onClick }: { f: FollowUp; onClick: () => void }) {
  const meta = metaFor(String(f.source ?? "other"));
  const account = (f.account ?? "").trim();
  // intent: "needs reply" | "fyi" | "question" | etc. Free-form from
  // the agent's triage; we just render whatever string it picked.
  const intent = metaString(f.metadata, "intent", "classification");
  // mode: "reply" | "read" | "skim" - the agent's recommended action.
  const mode = metaString(f.metadata, "mode", "action");

  return (
    <TileCard onClick={onClick}>
      <span
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-md border border-border bg-muted",
          meta.tone,
        )}
      >
        <meta.Icon className="size-4" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-foreground">{f.from}</span>
          {f.subject ? (
            <span className="hidden truncate text-xs text-muted-foreground sm:inline">
              · {f.subject}
            </span>
          ) : null}
          <span
            className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground"
            suppressHydrationWarning
          >
            {relTime(f.receivedAt)}
          </span>
        </div>
        {account || intent || mode ? (
          <div className="mt-1 flex flex-wrap items-center gap-1">
            {account ? <Chip tone="muted">{account}</Chip> : null}
            {intent ? <Chip tone={intentTone(intent)}>{intent}</Chip> : null}
            {mode ? <Chip tone={modeTone(mode)}>{mode}</Chip> : null}
          </div>
        ) : f.preview ? (
          // No chips yet - fall back to the preview so the row never
          // collapses to just "name + time". Agent recipes that haven't
          // started classifying still read.
          <p className="line-clamp-1 break-words text-[12px] text-muted-foreground">
            {f.preview}
          </p>
        ) : null}
      </div>
      {f.unread ? (
        <span
          aria-label="unread"
          className="size-2 shrink-0 rounded-full bg-info shadow-[0_0_6px_hsl(var(--info))]"
        />
      ) : null}
    </TileCard>
  );
}

// Chip - small caps pill. Tone tints the bg/text for known signals
// (reply / urgent etc) and stays neutral for free-form strings.
function Chip({
  children,
  tone = "muted",
}: {
  children: React.ReactNode;
  tone?: "muted" | "info" | "warn" | "success" | "danger";
}) {
  const cls = {
    muted: "bg-muted text-muted-foreground",
    info: "bg-info/15 text-info",
    warn: "bg-amber-500/15 text-amber-400",
    success: "bg-success/15 text-success",
    danger: "bg-danger/15 text-danger",
  }[tone];
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded-full px-2 font-mono text-[10px] uppercase tracking-wider",
        cls,
      )}
    >
      {children}
    </span>
  );
}

function intentTone(v: string): "muted" | "info" | "warn" | "success" | "danger" {
  const k = v.toLowerCase();
  if (k.includes("urgent") || k.includes("critical")) return "danger";
  if (k.includes("needs") || k.includes("waiting") || k.includes("reply")) return "warn";
  if (k.includes("fyi") || k.includes("info")) return "info";
  if (k.includes("done") || k.includes("resolved")) return "success";
  return "muted";
}

function modeTone(v: string): "muted" | "info" | "warn" | "success" | "danger" {
  const k = v.toLowerCase();
  if (k === "reply" || k.includes("reply")) return "warn";
  if (k === "read" || k.includes("read")) return "info";
  if (k.includes("skim") || k.includes("scan")) return "muted";
  if (k.includes("ignore") || k.includes("dismiss")) return "muted";
  return "muted";
}
