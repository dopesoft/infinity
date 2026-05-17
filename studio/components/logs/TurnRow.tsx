"use client";

import { type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { ArrowDownToLine, ArrowUpFromLine, Clock, type LucideIcon, Wrench } from "lucide-react";
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

export function TurnRow({ turn }: { turn: TurnRowDTO }) {
  const router = useRouter();
  const hasTokens = !!(turn.input_tokens || turn.output_tokens);
  return (
    <button
      type="button"
      onClick={() => router.push(`/logs/${turn.id}`)}
      className={cn(
        "group block w-full min-w-0 rounded-xl border bg-card px-3 py-2.5 text-left transition-colors",
        "hover:bg-accent focus:outline-none focus-visible:ring-1 focus-visible:ring-info",
      )}
    >
      {/* meta row - same shape as /trust card top line */}
      <div className="flex items-center justify-between gap-2 font-mono text-[11px] text-muted-foreground">
        <span className="flex min-w-0 items-center gap-1.5">
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

      {/* prompt */}
      <p className="mt-1.5 line-clamp-2 break-words text-sm font-semibold text-foreground">
        {turn.user_text || <span className="text-muted-foreground">(resumed turn)</span>}
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
  children,
}: {
  icon?: LucideIcon;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className="inline-flex min-w-0 items-center gap-1 rounded-md border border-border/60 bg-muted/40 px-1.5 py-0.5 font-mono text-foreground/80"
    >
      {Icon && <Icon className="size-3 shrink-0 text-muted-foreground" aria-hidden />}
      <span className="truncate">{children}</span>
    </span>
  );
}
