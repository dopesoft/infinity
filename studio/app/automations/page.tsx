"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { Button } from "@/components/ui/button";
import { ListRow } from "@/components/ui/list-row";
import { DayRibbon } from "@/components/ui/day-ribbon";
import { clockTime } from "@/lib/dashboard/format";
import { EmptyState } from "@/components/EmptyState";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { CronCreateCard, SentinelCreateCard } from "@/components/cron/CreateForms";
import type { RibbonMark } from "@/lib/schedule/ribbon";
import {
  fetchCrons,
  fetchSentinels,
  setCronEnabled,
  triggerCron,
  type CronJobDTO,
  type SentinelDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

/**
 * Automations — the next twenty four hours, drawn once, then one list.
 *
 * WHAT CHANGED, AND WHY
 *
 * This page was three tabs: Workflows, Cron, Sentinels. Three names from
 * three different worlds (enterprise software, a 1975 Unix daemon, and a
 * fantasy novel) over one idea: things that fire without you asking.
 *
 * And the tabs answered the wrong question. You do not open this page to
 * read a list, you open it to find out whether tonight is covered. That is a
 * question about TIME, so the answer is a timeline: the next day across the
 * top with the red mark showing where it went wrong last night, then
 * everything below in one list ordered by what needs you first.
 *
 * Schedules and watchers stopped being separate tabs because on a timeline
 * they are the same thing: a mark in the future.
 *
 * HYDRATION: `now` starts null and is filled in an effect, never in a
 * useState initializer, so the server and the first client paint agree.
 */
export default function AutomationsPage() {
  const router = useRouter();
  const [crons, setCrons] = useState<CronJobDTO[]>([]);
  const [sentinels, setSentinels] = useState<SentinelDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [now, setNow] = useState<number | null>(null);
  // Making a new automation is a sheet over this page, not a trip to another
  // route. Two kinds, because a schedule and a watcher genuinely take
  // different fields — same test as everywhere: different shape, different
  // form.
  const [creating, setCreating] = useState<null | "schedule" | "watcher">(null);

  useEffect(() => {
    setNow(Date.now());
    const t = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(t);
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    const [c, s] = await Promise.all([fetchCrons(), fetchSentinels()]);
    setCrons(c ?? []);
    setSentinels(s ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime(["mem_crons", "mem_sentinels"], load);

  const marks = useMemo<RibbonMark[]>(
    () =>
      crons
        .filter((c) => c.enabled && c.next_run_at)
        .map((c) => ({
          id: c.id,
          // The TIME, not the name. The name is the row title six lines
          // below; printing it twice made the ribbon a second copy of the
          // list and pushed a whole sentence off the left edge.
          label: clockTime(c.next_run_at as string),
          at: new Date(c.next_run_at as string).getTime(),
          tone: c.last_run_status === "error" ? ("danger" as const) : ("default" as const),
        })),
    [crons],
  );

  // Ordered by what needs you first: broken, then running, then the rest by
  // when they next fire. Paused sinks to the bottom — it is not waiting on
  // anything.
  const rows = useMemo(() => {
    const scored = crons.map((c) => ({ c, rank: rankOf(c) }));
    scored.sort((a, b) => a.rank - b.rank || nextMs(a.c) - nextMs(b.c));
    return scored.map((s) => s.c);
  }, [crons]);

  const failing = crons.filter((c) => c.last_run_status === "error").length;

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto scroll-touch">
        <div className="mx-auto flex w-full min-w-0 max-w-list flex-col gap-4 px-4 py-5 sm:px-6">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <h1 className="font-voice text-[26px] font-medium tracking-tight">Automations</h1>
              <p className="text-[12.5px] text-muted-foreground">
                {summaryLine(crons.length + sentinels.length, failing, loading)}
              </p>
            </div>
            <Button size="sm" className="h-9 shrink-0 gap-1.5" onClick={() => setCreating("schedule")}>
              <Plus className="size-4" aria-hidden />
              <span className="hidden sm:inline">New</span>
            </Button>
          </div>

          <DayRibbon now={now} marks={marks} />

          <div className="flex min-w-0 flex-col">
            {rows.map((c) => (
              <ListRow
                key={c.id}
                tone={toneOf(c)}
                live={c.last_run_status === "running"}
                title={humanName(c)}
                meta={metaOf(c, now)}
                trailing={
                  c.last_run_status === "error" ? (
                    <Button
                      size="sm"
                      variant="outline"
                      className="h-7 px-2.5 text-[11px]"
                      onClick={(e) => {
                        e.stopPropagation();
                        void triggerCron(c.id);
                      }}
                    >
                      Try again
                    </Button>
                  ) : !c.enabled ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 px-2.5 text-[11px]"
                      onClick={(e) => {
                        e.stopPropagation();
                        void setCronEnabled(c.id, true).then(load);
                      }}
                    >
                      Resume
                    </Button>
                  ) : (
                    <span className="font-mono text-[11px] tabular-nums text-quiet">
                      {whenNext(c, now)}
                    </span>
                  )
                }
                onClick={() => router.push(`/cron?focus=${encodeURIComponent(c.id)}`)}
              />
            ))}

            {sentinels.map((s) => (
              <ListRow
                key={s.id}
                tone={s.enabled ? "default" : "quiet"}
                title={s.name}
                meta={
                  s.fire_count > 0
                    ? `Fired ${s.fire_count} time${s.fire_count === 1 ? "" : "s"}`
                    : "Watching, never fired"
                }
                trailing={
                  <span className="font-mono text-[11px] text-quiet">{s.enabled ? "on" : "off"}</span>
                }
                onClick={() => router.push(`/cron?tab=sentinel&focus=${encodeURIComponent(s.id)}`)}
              />
            ))}

            {!loading && crons.length + sentinels.length === 0 && (
              <EmptyState
                icon={Plus}
                title="He does nothing on his own yet"
                description="Tell him something like “check my inbox every morning at six” and it will show up here, on the line above."
              />
            )}
            {loading && crons.length === 0 && (
              <p className="py-6 text-sm text-muted-foreground">Loading…</p>
            )}
          </div>
        </div>
      </div>

      <ResponsiveModal
        open={creating !== null}
        onOpenChange={(o) => setCreating(o ? (creating ?? "schedule") : null)}
        title={creating === "watcher" ? "Watch for something" : "Do this on a schedule"}
        description={
          creating === "watcher"
            ? "He checks for a condition and acts when it is met."
            : "He runs this without being asked, on the clock you set."
        }
        size="lg"
      >
        <div className="flex min-w-0 flex-col gap-3">
          <div className="flex gap-1.5">
            <Button
              size="sm"
              variant={creating === "schedule" ? "default" : "outline"}
              className="h-8 text-[12px]"
              onClick={() => setCreating("schedule")}
            >
              On a schedule
            </Button>
            <Button
              size="sm"
              variant={creating === "watcher" ? "default" : "outline"}
              className="h-8 text-[12px]"
              onClick={() => setCreating("watcher")}
            >
              Watch for something
            </Button>
          </div>
          {creating === "watcher" ? (
            <SentinelCreateCard
              onCreated={() => {
                setCreating(null);
                void load();
              }}
            />
          ) : (
            <CronCreateCard
              onCreated={() => {
                setCreating(null);
                void load();
              }}
            />
          )}
        </div>
      </ResponsiveModal>
    </AppShell>
  );
}

/**
 * The readable name. A cron's stored `name` is a slug and its `target` is a
 * skill id; `schedule_natural` is the sentence a person wrote. Prefer the
 * sentence, fall back to a de-slugged name — never show the cron expression
 * on a row. It lives in the detail, which is where you want it when
 * something is firing at the wrong time.
 */
function humanName(c: CronJobDTO): string {
  const natural = c.schedule_natural?.trim();
  if (natural && natural.length > 3) return natural;
  const s = c.name.replace(/[-_]+/g, " ").trim();
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** ONE line. Why it matters beats when it ran. */
function metaOf(c: CronJobDTO, now: number | null): string {
  if (c.last_run_status === "error") {
    return c.failure_count > 1
      ? `Failed ${c.failure_count} times running. I could not fix it myself.`
      : "It failed last time and I could not fix it myself.";
  }
  if (c.last_run_status === "running") return "Running now";
  if (!c.enabled) return "You paused this";
  if (c.last_run_at && now) return `Last ran ${ago(new Date(c.last_run_at).getTime(), now)}`;
  return "Has not run yet";
}

function toneOf(c: CronJobDTO): "default" | "brand" | "danger" | "quiet" {
  if (c.last_run_status === "error") return "danger";
  if (c.last_run_status === "running") return "brand";
  if (!c.enabled) return "quiet";
  return "default";
}

function rankOf(c: CronJobDTO): number {
  if (c.last_run_status === "error") return 0;
  if (c.last_run_status === "running") return 1;
  if (!c.enabled) return 3;
  return 2;
}

function nextMs(c: CronJobDTO): number {
  return c.next_run_at ? new Date(c.next_run_at).getTime() : Number.MAX_SAFE_INTEGER;
}

function whenNext(c: CronJobDTO, now: number | null): string {
  if (!c.next_run_at || !now) return "";
  const delta = new Date(c.next_run_at).getTime() - now;
  if (delta < 0) return "due";
  const mins = Math.round(delta / 60_000);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `in ${hours}h`;
  return `in ${Math.round(hours / 24)}d`;
}

function ago(then: number, now: number): string {
  const mins = Math.round((now - then) / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

/**
 * The one line under the title. It answers the question you arrived with
 * before you read a single row — and it never says "all good" when something
 * is broken.
 */
function summaryLine(total: number, failing: number, loading: boolean): string {
  if (loading && total === 0) return "Loading…";
  if (total === 0) return "Nothing runs on its own yet.";
  if (failing > 0) {
    return `${total} running. ${failing === 1 ? "One failed" : `${failing} failed`} and I could not fix ${failing === 1 ? "it" : "them"} myself.`;
  }
  return `${total} running. Everything went through last night.`;
}
