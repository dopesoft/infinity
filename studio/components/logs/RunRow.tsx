"use client";

import { Clock, Timer } from "lucide-react";
import { cn } from "@/lib/utils";
import type { RunDTO } from "@/lib/api";

/* RunRow - one row in the /logs "Runs" lens.
 *
 * The durable record of a server-tracked action (mem_runs): every cron firing,
 * skill invoke, heartbeat scan, voyager/GEPA run, etc. This is where a Done
 * card on the dashboard kanban lives on AFTER it ages out of "today" - the boss
 * can come back days later and still read what each run did and how it went.
 *
 * Card shape matches TurnRow exactly so /logs reads as one family: mono meta
 * line (time · engine · status pip) on top, the run's narrative
 * (result_summary) as the body, duration + failure explanation at the bottom.
 */

// Raw mem_runs.kind → the readable subsystem name the boss recognises. Mirrors
// the Engine labels the dashboard kanban uses so the two surfaces agree.
const ENGINE_LABEL: Record<string, string> = {
  cron: "Schedule",
  skill: "Skill",
  heartbeat: "Heartbeat",
  "voyager.optimize": "GEPA",
  "voyager.extract": "Voyager",
  "gym.extract": "Gym",
  sentinel: "Sentinel",
  "browser.session": "Browser",
  "extension.register": "Extension",
  "background.build": "Build",
  "surface.action": "Action",
};

function engineOf(kind: string): string {
  return ENGINE_LABEL[kind] ?? humanize(kind);
}

// kebab/snake/dotted machine name → readable title, dropping a trailing
// all-digit id token (the science-experiment suffix). Mirrors the Go
// humanizeName in core/internal/dashboard/api.go.
function humanize(raw: string): string {
  const parts = raw.replace(/[-_.]+/g, " ").trim().split(/\s+/).filter(Boolean);
  if (parts.length > 1 && /^\d+$/.test(parts[parts.length - 1])) parts.pop();
  const s = parts.join(" ");
  return s ? s[0].toUpperCase() + s.slice(1) : raw;
}

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

function formatDuration(ms?: number): string {
  if (!ms || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.round(s / 60)}m ${Math.round(s % 60)}s`;
}

const STATUS_STYLE: Record<RunDTO["status"], string> = {
  running: "bg-info/15 text-info border-info/30",
  ok: "bg-success/10 text-success border-success/30",
  error: "bg-danger/10 text-danger border-danger/30",
};

const STATUS_LABEL: Record<RunDTO["status"], string> = {
  running: "running",
  ok: "ok",
  error: "failed",
};

export function RunRow({ run }: { run: RunDTO }) {
  const summary = run.result_summary?.trim();
  const failTitle = run.human_error?.title?.trim();
  const failSummary = run.human_error?.summary?.trim();
  return (
    <div
      className={cn(
        "block w-full min-w-0 rounded-xl border bg-card px-3 py-2.5 text-left",
      )}
    >
      {/* meta row */}
      <div className="flex items-center justify-between gap-2 font-mono text-[11px] text-muted-foreground">
        <span className="flex min-w-0 items-center gap-1.5">
          <Clock className="size-3 shrink-0" aria-hidden />
          <time className="shrink-0" suppressHydrationWarning>
            {formatTime(run.started_at)}
          </time>
          <span className="truncate uppercase tracking-wider">· {engineOf(run.kind)}</span>
        </span>
        <span
          className={cn(
            "shrink-0 rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider",
            STATUS_STYLE[run.status],
          )}
        >
          {STATUS_LABEL[run.status]}
        </span>
      </div>

      {/* what ran */}
      <p className="mt-1.5 break-words text-sm font-semibold text-foreground">
        {humanize(run.label || run.target_id || run.kind)}
      </p>

      {/* the narrative the run wrote - preserves the header/body line break */}
      {summary ? (
        <p className="mt-1 whitespace-pre-wrap break-words text-[12px] leading-snug text-muted-foreground">
          {summary}
        </p>
      ) : null}

      {/* failure, in plain language */}
      {run.status === "error" ? (
        <p className="mt-1 break-words text-[12px] leading-snug text-danger">
          {failTitle || run.error || "failed"}
          {failSummary ? <span className="text-danger/80"> — {failSummary}</span> : null}
        </p>
      ) : null}

      {/* bottom meta */}
      {run.duration_ms ? (
        <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground">
          <span className="inline-flex items-center gap-1 rounded-md border border-border/60 bg-muted/40 px-1.5 py-0.5 font-mono text-foreground/80">
            <Timer className="size-3 shrink-0 text-muted-foreground" aria-hidden />
            {formatDuration(run.duration_ms)}
          </span>
        </div>
      ) : null}
    </div>
  );
}
