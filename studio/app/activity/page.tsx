"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Activity as ActivityIcon } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { Button } from "@/components/ui/button";
import { CountLine } from "@/components/ui/count-line";
import { Timeline, TimelineDay, TimelineRow } from "@/components/ui/timeline";
import { EmptyState } from "@/components/EmptyState";
import { useRuns } from "@/lib/runs/useRuns";
import { useRealtime } from "@/lib/realtime/provider";
import { useTabParam } from "@/lib/useTabParam";
import {
  fetchHeartbeatFindings,
  fetchTraces,
  runHeartbeatNow,
  type HeartbeatFindingDTO,
  type RunDTO,
  type TurnRowDTO,
} from "@/lib/api";

/**
 * Activity — one river, ordered by time.
 *
 * WHAT CHANGED, AND WHY
 *
 * This was two pages. /heartbeat was "what he noticed" and /logs was "what
 * he did", and you had to guess which one held the thing you were looking
 * for. They were never two questions. Worse, both were named after
 * mechanisms: a heartbeat is a health check, and "turns" and "runs" are
 * agent-loop internals that leaked into a tab strip.
 *
 * Time is the only sensible order for this, so time is the structure: one
 * column, one rail, the clock down the left in mono so your eye lands on
 * WHEN before WHAT. Red in the rail is the only decoration on the page.
 *
 * The counts across the top are a <CountLine>, and the first one you read is
 * how many things he could not finish — because on most days that is the
 * only reason you opened this page.
 */

const LENSES = ["all", "failed", "jobs", "chats", "noticed"] as const;
type Lens = (typeof LENSES)[number];

/** One thing that happened, whatever it was underneath. */
type Event = {
  id: string;
  at: number;
  tone: "default" | "brand" | "warning" | "danger";
  live?: boolean;
  title: string;
  meta?: string;
  trailing?: string;
  lens: Exclude<Lens, "all" | "failed">;
  failed: boolean;
  href?: string;
};

export default function ActivityPage() {
  const router = useRouter();
  const [lens, setLens] = useTabParam<Lens>("lens", "all", LENSES);
  const [turns, setTurns] = useState<TurnRowDTO[]>([]);
  const [findings, setFindings] = useState<HeartbeatFindingDTO[]>([]);
  const [checking, setChecking] = useState(false);
  const { runs } = useRuns({ limit: 200 });

  const load = useCallback(async () => {
    const [t, f] = await Promise.all([fetchTraces({ limit: 200 }), fetchHeartbeatFindings(100)]);
    setTurns(t ?? []);
    setFindings(f ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime(["mem_turns", "mem_heartbeat_findings"], load);

  const events = useMemo<Event[]>(() => {
    const out: Event[] = [];

    for (const r of runs) out.push(fromRun(r));
    for (const t of turns) out.push(fromTurn(t));
    for (const f of findings) out.push(fromFinding(f));

    out.sort((a, b) => b.at - a.at);
    return out;
  }, [runs, turns, findings]);

  const counts = useMemo(
    () => ({
      failed: events.filter((e) => e.failed).length,
      jobs: events.filter((e) => e.lens === "jobs").length,
      chats: events.filter((e) => e.lens === "chats").length,
      noticed: events.filter((e) => e.lens === "noticed").length,
    }),
    [events],
  );

  const shown = useMemo(() => {
    if (lens === "all") return events;
    if (lens === "failed") return events.filter((e) => e.failed);
    return events.filter((e) => e.lens === lens);
  }, [events, lens]);

  const days = useMemo(() => groupByDay(shown), [shown]);

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto scroll-touch">
        <div className="mx-auto flex w-full min-w-0 max-w-list flex-col gap-4 px-4 py-5 sm:px-6">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <h1 className="font-voice text-[26px] font-medium tracking-tight">Activity</h1>
              <p className="text-[12.5px] text-muted-foreground">
                Everything I did, and everything I noticed. Red means I could not finish something.
              </p>
            </div>
            <Button
              size="sm"
              variant="outline"
              className="h-9 shrink-0"
              disabled={checking}
              onClick={async () => {
                setChecking(true);
                await runHeartbeatNow();
                await load();
                setChecking(false);
              }}
            >
              {checking ? "Checking…" : "Check now"}
            </Button>
          </div>

          <CountLine
            items={[
              { value: "", label: "everything", selected: lens === "all", onSelect: () => setLens("all") },
              { value: counts.failed, label: "could not finish", tone: "danger", selected: lens === "failed", onSelect: () => setLens("failed") },
              { value: counts.jobs, label: "jobs", selected: lens === "jobs", onSelect: () => setLens("jobs") },
              { value: counts.chats, label: "conversations", selected: lens === "chats", onSelect: () => setLens("chats") },
              { value: counts.noticed, label: "I noticed", selected: lens === "noticed", onSelect: () => setLens("noticed") },
            ]}
          />

          {shown.length === 0 ? (
            <EmptyState
              icon={ActivityIcon}
              // The page subtitle already says what lands here.
              title="Nothing here yet"
            />
          ) : (
            <Timeline>
              {days.map(([day, list]) => (
                <div key={day} className="flex min-w-0 flex-col">
                  <TimelineDay label={day} />
                  {list.map((e) => (
                    <TimelineRow
                      key={e.id}
                      time={clock(e.at)}
                      tone={e.tone}
                      live={e.live}
                      title={e.title}
                      meta={e.meta}
                      trailing={e.trailing}
                      onClick={e.href ? () => router.push(e.href as string) : undefined}
                    />
                  ))}
                </div>
              ))}
            </Timeline>
          )}
        </div>
      </div>
    </AppShell>
  );
}

/**
 * A run reads as what he DID, in his words. `result_summary` is the
 * narrative the run ledger already carries; `human_error` is the plain
 * translation of a failure. Falling back to the raw label is the last
 * resort, never the first choice.
 */
function fromRun(r: RunDTO): Event {
  const failed = r.status === "error";
  return {
    id: `run:${r.id}`,
    at: new Date(r.started_at).getTime(),
    tone: failed ? "danger" : r.status === "running" ? "brand" : "default",
    live: r.status === "running",
    title: failed
      ? (r.human_error?.title ?? r.result_summary ?? `${r.label} could not finish`)
      : (r.result_summary ?? r.label),
    meta: failed ? r.human_error?.summary : r.progress_label,
    trailing: r.duration_ms ? duration(r.duration_ms) : undefined,
    lens: "jobs",
    failed,
    href: undefined,
  };
}

function fromTurn(t: TurnRowDTO): Event {
  // "errored" and "interrupted" both mean the turn did not land. An
  // interrupted turn is not a crash, but it IS unfinished, and a page whose
  // job is "what could not finish" must not quietly call it done.
  const failed = t.status === "errored" || t.status === "interrupted";
  return {
    id: `turn:${t.id}`,
    at: new Date(t.started_at).getTime(),
    tone: failed ? "danger" : "default",
    title: t.summary?.trim() || t.user_text?.trim() || "A conversation",
    meta:
      t.status === "interrupted"
        ? "Interrupted before it finished"
        : t.status === "errored"
          ? (t.error ?? "It did not finish")
          : t.session_name,
    trailing: undefined,
    lens: "chats",
    failed,
    href: `/logs/${encodeURIComponent(t.id)}`,
  };
}

function fromFinding(f: HeartbeatFindingDTO): Event {
  return {
    id: `find:${f.id}`,
    at: new Date(f.started_at).getTime(),
    tone: "warning",
    title: f.title,
    meta: f.detail?.split("\n")[0],
    lens: "noticed",
    failed: false,
  };
}

/** Short casual time, never 24h with seconds and never ISO. */
function clock(ms: number): string {
  return new Date(ms)
    .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
    .toLowerCase()
    .replace(/\s/g, "");
}

function duration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return s ? `${m}m ${s}s` : `${m}m`;
}

/**
 * "Today" / "Yesterday" / a date. Computed from the events themselves rather
 * than from Date.now() at module scope, so it cannot drift across a midnight
 * boundary while the page is open.
 */
function groupByDay(events: Event[]): [string, Event[]][] {
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const dayMs = 86_400_000;
  const out = new Map<string, Event[]>();
  for (const e of events) {
    let label: string;
    if (e.at >= startOfToday) label = "Today";
    else if (e.at >= startOfToday - dayMs) label = "Yesterday";
    else label = new Date(e.at).toLocaleDateString([], { weekday: "long", month: "short", day: "numeric" });
    const list = out.get(label);
    if (list) list.push(e);
    else out.set(label, [e]);
  }
  return [...out.entries()];
}
