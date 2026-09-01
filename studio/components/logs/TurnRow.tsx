"use client";

import { type ReactNode } from "react";
import { useAppRouter } from "@/lib/loading";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Clock,
  DatabaseZap,
  type LucideIcon,
  Wrench,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { TurnStatusPip } from "./TurnStatusPip";
import type { TurnRowDTO } from "@/lib/api";

/* TurnRow - one row in the /logs list.
 *
 * Card shape matches the rest of the app (rounded-xl border bg-card,
 * hover:bg-accent). Top line is meta (timestamp · session · model · pip)
 * in monospace, middle is the prompt, bottom is tool count + tokens +
 * latency. Tapping the card pushes to /logs/<id>.
 */
function formatTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const yest = new Date(now);
  yest.setDate(yest.getDate() - 1);
  const isYesterday = d.toDateString() === yest.toDateString();
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (sameDay) return `Today ${time}`;
  if (isYesterday) return `Yesterday ${time}`;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function shortTurnId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

function formatLatency(ms: number): string {
  if (!ms || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.round(s / 60)}m ${Math.round(s % 60)}s`;
}

// OriginBadge marks WHERE a turn came from — a scheduled cron, the heartbeat,
// etc. — so a cron run is unmistakable in /logs instead of reading like a chat
// message. Chat turns render no badge (the default), keeping the list clean.
function OriginBadge({ kind, label }: { kind?: string; label?: string }) {
  const k = (kind || "chat").toLowerCase();
  if (k === "chat" || k === "") return null;
  const style: Record<string, string> = {
    cron: "border-info/40 bg-info/10 text-info",
    heartbeat: "border-warning/40 bg-warning/10 text-warning",
    sentinel: "border-warning/40 bg-warning/10 text-warning",
  };
  const cls = style[k] ?? "border-border bg-muted text-muted-foreground";
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-full border px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider",
        cls,
      )}
    >
      {k}
      {label ? <span className="font-normal normal-case opacity-80">· {label}</span> : null}
    </span>
  );
}

export function TurnRow({ turn }: { turn: TurnRowDTO }) {
  const router = useAppRouter();
  const hasTokens = !!(turn.input_tokens || turn.output_tokens);
  const isChat = !turn.session_kind || turn.session_kind.toLowerCase() === "chat";
  return (
    <button
      type="button"
      onClick={() => router.push(`/logs/${turn.id}`)}
      className={cn(
        "group block w-full min-w-0 rounded-xl border bg-card px-3 py-2.5 text-left transition-colors",
        "hover:bg-accent focus:outline-none focus-visible:ring-1 focus-visible:ring-info",
        // Non-chat origins (cron/heartbeat) get a tinted left edge so they're
        // scannable at a glance, not just on the badge.
        !isChat && "border-l-2 border-l-info/50",
      )}
    >
      {/* meta row - same shape as /trust card top line */}
      <div className="flex items-center justify-between gap-2 font-mono text-[11px] text-muted-foreground">
        <span className="flex min-w-0 items-center gap-1.5">
          <OriginBadge kind={turn.session_kind} label={turn.origin_label} />
          <Clock className="size-3 shrink-0" aria-hidden />
          <time className="shrink-0" suppressHydrationWarning>
            {formatTime(turn.started_at)}
          </time>
          {turn.session_name && (
            <span className="truncate">· {turn.session_name}</span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          {turn.model && (
            <span className="hidden uppercase tracking-wider sm:inline">{turn.model}</span>
          )}
          <TurnStatusPip status={turn.status} />
        </span>
      </div>

      {/* prompt (for a cron, this is the cron's instruction) */}
      <p className="mt-1.5 line-clamp-2 break-words text-sm font-semibold text-foreground">
        {turn.user_text || (
          <span className="text-muted-foreground">
            {isChat ? "(resumed turn)" : `(${turn.session_kind} run)`}
          </span>
        )}
      </p>

      {/* summary */}
      {turn.summary && (
        <p className="mt-0.5 line-clamp-1 text-[11px] text-muted-foreground">{turn.summary}</p>
      )}

      {/* bottom meta */}
      <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground">
        <MetricChip title={`turn id ${turn.id}`}>
          <span className="select-all">{shortTurnId(turn.id)}</span>
        </MetricChip>
        {turn.tool_call_count > 0 && (
          <MetricChip icon={Wrench} title="Tool calls">
            {turn.tool_call_count}
          </MetricChip>
        )}
        {hasTokens && (
          <MetricChip icon={ArrowDownToLine} title="Input tokens (prompt sent to model)">
            {turn.input_tokens.toLocaleString()}
          </MetricChip>
        )}
        {hasTokens && (
          <MetricChip icon={ArrowUpFromLine} title="Output tokens (model reply)">
            {turn.output_tokens.toLocaleString()}
          </MetricChip>
        )}
        {!!turn.cache_read_tokens && turn.cache_read_tokens > 0 && turn.input_tokens > 0 && (
          <MetricChip
            icon={DatabaseZap}
            positive
            title={`${turn.cache_read_tokens.toLocaleString()} prompt tokens served from cache at ~0.1x cost`}
          >
            {Math.round((turn.cache_read_tokens / turn.input_tokens) * 100)}% cached
          </MetricChip>
        )}
        {turn.latency_ms > 0 && (
          <MetricChip icon={Clock} title="Latency">
            {formatLatency(turn.latency_ms)}
          </MetricChip>
        )}
        {turn.error && (
          <span className="truncate text-danger normal-case tracking-normal">{turn.error}</span>
        )}
      </div>
    </button>
  );
}

function MetricChip({
  icon: Icon,
  title,
  positive = false,
  children,
}: {
  icon?: LucideIcon;
  title?: string;
  positive?: boolean;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex min-w-0 items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono",
        positive
          ? "border-success/40 bg-success/10 text-success"
          : "border-border/60 bg-muted/40 text-foreground/80",
      )}
    >
      {Icon && (
        <Icon
          className={cn("size-3 shrink-0", positive ? "text-success" : "text-muted-foreground")}
          aria-hidden
        />
      )}
      <span className="truncate">{children}</span>
    </span>
  );
}
