"use client";

import { useRouter } from "next/navigation";
import { Clock, Wrench } from "lucide-react";
import { cn } from "@/lib/utils";
import { TurnStatusPip } from "./TurnStatusPip";
import type { TurnRowDTO } from "@/lib/api";

/* TurnRow — one row in the /logs list.
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

function formatLatency(ms: number): string {
  if (!ms || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.round(s / 60)}m ${Math.round(s % 60)}s`;
}

export function TurnRow({ turn }: { turn: TurnRowDTO }) {
  const router = useRouter();
  const tokens =
    turn.input_tokens || turn.output_tokens
      ? `${turn.input_tokens.toLocaleString()} → ${turn.output_tokens.toLocaleString()}`
      : "";
  return (
    <button
      type="button"
      onClick={() => router.push(`/logs/${turn.id}`)}
      className={cn(
        "group block w-full min-w-0 rounded-xl border bg-card px-3 py-2.5 text-left transition-colors",
        "hover:bg-accent focus:outline-none focus-visible:ring-1 focus-visible:ring-info",
      )}
    >
      {/* meta row — same shape as /trust card top line */}
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
      <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        {turn.tool_call_count > 0 && (
          <span className="inline-flex items-center gap-1">
            <Wrench className="size-3" aria-hidden />
            {turn.tool_call_count}
          </span>
        )}
        {tokens && <span>{tokens} tok</span>}
        {turn.latency_ms > 0 && <span>{formatLatency(turn.latency_ms)}</span>}
        {turn.error && (
          <span className="truncate text-danger normal-case tracking-normal">{turn.error}</span>
        )}
      </div>
    </button>
  );
}
