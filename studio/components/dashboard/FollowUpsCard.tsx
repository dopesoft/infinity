"use client";

import { AtSign, Hash, Inbox, Workflow, MessageCircle, type LucideIcon } from "lucide-react";
import { Section, TileCard } from "./Section";
import { cn } from "@/lib/utils";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, FollowUp, FollowUpSource } from "@/lib/dashboard/types";

/* Follow-ups — humans (or connector-surfaced systems) waiting on you.
 *
 * Different from Approvals (which are agent-waiting-on-you). These rows
 * are emails, Slack mentions, iMessage threads, Linear pings that the
 * agent has flagged as needing a response. Tapping opens the ObjectViewer
 * with the full content so you can decide if it's worth a conversation
 * — that's the design rule: preview first, then "Discuss with Jarvis".
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
      <div className="space-y-2">
        {followUps.length === 0 ? (
          <div className="rounded-xl border border-dashed bg-card/30 p-4 text-center text-xs text-muted-foreground">
            Inbox zero — no one is waiting on you.
          </div>
        ) : (
          <ul className="space-y-1.5">
            {followUps.map((f) => (
              <li key={f.id}>
                <FollowUpRow f={f} onClick={() => onOpen({ kind: "followup", data: f })} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

function FollowUpRow({ f, onClick }: { f: FollowUp; onClick: () => void }) {
  const meta = SOURCE_META[f.source] ?? SOURCE_META.other;
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
        <p className="line-clamp-1 break-words text-[12px] text-muted-foreground">{f.preview}</p>
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
