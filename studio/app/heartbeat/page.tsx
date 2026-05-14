"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Activity,
  AlertTriangle,
  Brain,
  Eye,
  Lightbulb,
  Play,
  RefreshCw,
  Shield,
  Sparkles,
  Wrench,
  Zap,
  MessageSquare,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import {
  decideCuriosityQuestion,
  fetchHeartbeats,
  fetchHeartbeatFindings,
  fetchIntentRecent,
  runHeartbeatNow,
  type HeartbeatRunDTO,
  type HeartbeatRunSummaryDTO,
  type HeartbeatFindingDTO,
  type IntentRecordDTO,
} from "@/lib/api";
import { seedSession } from "@/lib/dashboard/seed";
import { useRealtime } from "@/lib/realtime/provider";

/* Heartbeat — system pulse monitor.
 *
 * This page is the diagnostic command center for the proactive engine: a live
 * status hero at the top with the actions baked in (not floating beneath as
 * orphans), an interleaved pulse stream of every event the heartbeat has
 * emitted on the left, and a compact run + kind sidebar on the right. The
 * mental model is "watch the agent's pulse" — every tick, every finding,
 * every classified turn lands here, time-ordered.
 *
 * Distinct from /memory by intent: memory is a library of facts, heartbeat is
 * a live monitor. Different shape, different rhythm. */

type EventKind = "run" | "finding" | "intent";
type EventFilter = "all" | "findings" | "intent" | "runs";

const EVENT_FILTERS: { key: EventFilter; label: string }[] = [
  { key: "all", label: "All pulses" },
  { key: "findings", label: "Findings" },
  { key: "intent", label: "Intent" },
  { key: "runs", label: "Runs" },
];

type PulseEvent = {
  id: string;
  kind: EventKind;
  at: string;
  run?: HeartbeatRunDTO;
  finding?: HeartbeatFindingDTO;
  intent?: IntentRecordDTO;
};

const FINDING_TONE: Record<string, string> = {
  outcome: "text-warning border-warning/40",
  pattern: "text-info border-info/40",
  curiosity: "text-info border-info/40",
  surprise: "text-success border-success/40",
  security: "text-danger border-danger/40",
  self_heal: "text-orange-500 border-orange-500/40",
};

const FINDING_ICON: Record<string, React.ComponentType<{ className?: string }>> = {
  surprise: Sparkles,
  curiosity: Lightbulb,
  security: Shield,
  outcome: AlertTriangle,
  pattern: Eye,
  self_heal: Wrench,
};

const INTENT_TONE: Record<IntentRecordDTO["token"], { label: string; cls: string; ring: string }> = {
  fast_intervention: {
    label: "FAST",
    cls: "text-info",
    ring: "ring-info/40",
  },
  silent: {
    label: "SILENT",
    cls: "text-muted-foreground",
    ring: "ring-muted-foreground/30",
  },
  full_assistance: {
    label: "FULL",
    cls: "text-primary",
    ring: "ring-primary/40",
  },
};

function relTime(iso?: string | null): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const s = Math.max(1, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function clockTime(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function formatInterval(seconds: number) {
  if (seconds <= 0) return "—";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

function inLast24h(iso: string): boolean {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return false;
  return Date.now() - t < 24 * 60 * 60 * 1000;
}

export default function HeartbeatPage() {
  const router = useRouter();
  const [intervalSeconds, setIntervalSeconds] = useState(0);
  const [runs, setRuns] = useState<HeartbeatRunDTO[]>([]);
  const [findings, setFindings] = useState<HeartbeatFindingDTO[]>([]);
  const [intents, setIntents] = useState<IntentRecordDTO[]>([]);
  const [running, setRunning] = useState(false);
  const [last, setLast] = useState<HeartbeatRunSummaryDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<EventFilter>("all");

  async function loadAll() {
    setLoading(true);
    const [r, f, i] = await Promise.all([
      fetchHeartbeats(),
      fetchHeartbeatFindings(100),
      fetchIntentRecent(100),
    ]);
    if (r) {
      setIntervalSeconds(r.interval_seconds);
      setRuns(r.runs);
    }
    setFindings(f ?? []);
    setIntents(i ?? []);
    setLoading(false);
  }

  useEffect(() => {
    loadAll();
  }, []);

  useRealtime(["mem_heartbeats", "mem_heartbeat_findings", "mem_intent_decisions"], loadAll);

  async function fireNow() {
    setRunning(true);
    const res = await runHeartbeatNow();
    setRunning(false);
    if (res) setLast(res);
    await loadAll();
  }

  async function discussFinding(f: HeartbeatFindingDTO) {
    if (!f.curiosity_id) return;
    await decideCuriosityQuestion(f.curiosity_id, "asked");
    const sessionId = await seedSession("curiosity", f.curiosity_id, {
      question: f.title,
      context: f.detail,
      source: "heartbeat",
      heartbeat_finding_id: f.id,
    });
    if (sessionId) router.push(`/live?session=${encodeURIComponent(sessionId)}`);
    else router.push("/live");
  }

  async function dismissFinding(f: HeartbeatFindingDTO) {
    if (!f.curiosity_id) return;
    const ok = await decideCuriosityQuestion(f.curiosity_id, "dismissed");
    if (!ok) return;
    setFindings((prev) =>
      prev.filter((x) => x.curiosity_id !== f.curiosity_id && x.title !== f.title),
    );
    await loadAll();
  }

  /* Stream: time-merged events. Runs that produced 0 findings still show up
   * (so the boss can see the pulse is alive even when nothing notable
   * happened) but they're visually de-emphasized. Findings inherit their
   * parent run's timestamp via the join in the backend query. */
  const stream = useMemo<PulseEvent[]>(() => {
    const events: PulseEvent[] = [];
    const seenFindings = new Set<string>();
    for (const r of runs) {
      events.push({ id: `run-${r.id}`, kind: "run", at: r.started_at, run: r });
    }
    for (const f of findings) {
      const key = `${f.kind}:${f.title}`;
      if (seenFindings.has(key)) continue;
      seenFindings.add(key);
      events.push({ id: `find-${f.id}`, kind: "finding", at: f.started_at, finding: f });
    }
    for (const i of intents) {
      events.push({ id: `int-${i.id}`, kind: "intent", at: i.created_at, intent: i });
    }
    events.sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime());
    return events;
  }, [runs, findings, intents]);

  const filteredStream = useMemo(() => {
    if (filter === "all") return stream;
    return stream.filter((e) => {
      if (filter === "findings") return e.kind === "finding";
      if (filter === "intent") return e.kind === "intent";
      if (filter === "runs") return e.kind === "run";
      return true;
    });
  }, [stream, filter]);

  const stats = useMemo(() => {
    const findings24h = findings.filter((f) => inLast24h(f.started_at)).length;
    const intents24h = intents.filter((i) => inLast24h(i.created_at)).length;
    const surprises = findings.filter((f) => f.kind === "surprise").length;
    const security = findings.filter((f) => f.kind === "security").length;
    return { findings24h, intents24h, surprises, security };
  }, [findings, intents]);

  const lastRun = runs[0];
  const isAlive = lastRun
    ? Date.now() - new Date(lastRun.started_at).getTime() < (intervalSeconds + 60) * 1000
    : false;

  /* Top-kind distribution for the sidebar mini-stats. Sorted by count desc,
   * limited to the kinds actually represented so empty kinds don't bloat
   * the strip. */
  const kindCounts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const f of findings) m[f.kind] = (m[f.kind] ?? 0) + 1;
    return Object.entries(m).sort((a, b) => b[1] - a[1]);
  }, [findings]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col bg-background">
        {/* Hero status card. Mobile-first: pulse + title/stats on top, action
            row spans full width below for 44px-min tap targets. sm+: actions
            float to the right of the header inline (same row). Never orphans
            the buttons — they're always anchored to a parent surface. */}
        <div className="border-b bg-gradient-to-b from-background to-background/40 px-3 py-4 sm:px-4 sm:py-6">
          <div className="mx-auto flex w-full max-w-5xl flex-col gap-3 sm:flex-row sm:items-center sm:gap-4">
            <div className="flex min-w-0 flex-1 items-center gap-3 sm:gap-4">
              <PulseIndicator alive={isAlive} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 sm:gap-x-3">
                  <h1 className="text-lg font-semibold tracking-tight text-foreground sm:text-xl">
                    Heartbeat
                  </h1>
                  <Badge
                    variant="outline"
                    className={cn(
                      "font-mono text-[10px]",
                      isAlive
                        ? "border-success/40 bg-success/10 text-success"
                        : "border-muted-foreground/40 text-muted-foreground",
                    )}
                  >
                    {isAlive ? "alive" : "quiet"}
                  </Badge>
                  <span className="text-xs text-muted-foreground">
                    every {formatInterval(intervalSeconds)}
                    {lastRun ? (
                      <>
                        {" · "}
                        <span suppressHydrationWarning>{relTime(lastRun.started_at)}</span>
                      </>
                    ) : null}
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted-foreground sm:gap-x-4">
                  <Stat icon={Activity} value={runs.length} label="runs" />
                  <Stat icon={Sparkles} value={stats.findings24h} label="findings 24h" />
                  <Stat icon={Zap} value={stats.intents24h} label="classified 24h" />
                  {stats.surprises > 0 && (
                    <Stat icon={Lightbulb} value={stats.surprises} label="surprises" />
                  )}
                  {stats.security > 0 && (
                    <Stat icon={Shield} value={stats.security} label="security" />
                  )}
                </div>
              </div>
            </div>
            {/* Action row. Full-width on mobile so Pulse now is a fat
                primary CTA (44px+ tap target); shrinks to icon-on-right
                on sm+. Refresh stays compact icon-only. */}
            <div className="flex shrink-0 items-stretch gap-2">
              <button
                type="button"
                onClick={fireNow}
                disabled={running}
                className="inline-flex h-11 flex-1 items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60 sm:h-10 sm:flex-none sm:text-xs"
              >
                <Play className="size-4 sm:size-3.5" aria-hidden />
                {running ? "Pulsing…" : "Pulse now"}
              </button>
              <button
                type="button"
                onClick={loadAll}
                disabled={loading}
                className="inline-flex size-11 shrink-0 items-center justify-center rounded-md border border-input bg-background text-foreground shadow-sm transition-colors hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50 sm:size-10"
                aria-label="Refresh"
                title="Refresh"
              >
                <RefreshCw className={cn("size-4", loading && "animate-spin")} aria-hidden />
              </button>
            </div>
          </div>
        </div>

        {/* Body: two-column on desktop, stacked on mobile. Left is the live
            pulse stream; right is a compact runs ledger + kind distribution. */}
        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          {/* Stream */}
          <main className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 py-4 scroll-touch sm:px-4 lg:px-6">
            <div className="mx-auto w-full max-w-3xl">
              {/* Filter pills. Mobile gets a horizontal snap-scroll row at
                  44px tap height; sm+ collapses to a tighter desktop bar. */}
              <div className="-mx-3 mb-3 flex snap-x snap-mandatory items-center gap-2 overflow-x-auto px-3 pb-1 scroll-touch sm:mx-0 sm:snap-none sm:px-0">
                {EVENT_FILTERS.map((f) => (
                  <button
                    key={f.key}
                    type="button"
                    onClick={() => setFilter(f.key)}
                    className={cn(
                      "inline-flex h-10 shrink-0 snap-start items-center rounded-full border px-4 text-sm font-medium transition-colors sm:h-8 sm:px-3 sm:text-xs",
                      filter === f.key
                        ? "border-foreground bg-foreground text-background"
                        : "border-border text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {f.label}
                  </button>
                ))}
              </div>

              {/* Live last-run findings banner. Shown only right after a
                  manual run, before the realtime push has merged them
                  into the persisted feed. */}
              {last && last.findings.length > 0 && (
                <div className="mb-4 rounded-xl border border-success/40 bg-success/5 p-3">
                  <div className="mb-2 flex items-center gap-2 text-[11px] uppercase tracking-wider text-success">
                    <Sparkles className="size-3" aria-hidden />
                    Latest pulse · {last.findings.length} findings
                  </div>
                  <ul className="space-y-1.5">
                    {last.findings.map((f, i) => (
                      <li key={i} className="text-sm">
                        <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                          {f.kind}
                        </span>{" "}
                        <span className="text-foreground">{f.title}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              {filteredStream.length === 0 ? (
                <EmptyPulse loading={loading} filter={filter} />
              ) : (
                <ol className="relative space-y-3 border-l border-border/60 pl-4">
                  {filteredStream.map((e) => (
                    <PulseRow
                      key={e.id}
                      ev={e}
                      onDiscuss={discussFinding}
                      onDismiss={dismissFinding}
                    />
                  ))}
                </ol>
              )}
            </div>
          </main>

          {/* Sidebar */}
          <aside className="shrink-0 border-t bg-muted/20 px-3 py-4 sm:px-4 lg:w-80 lg:border-l lg:border-t-0 lg:py-6">
            <div className="space-y-5">
              <section>
                <h2 className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                  Recent ticks
                </h2>
                {runs.length === 0 ? (
                  <p className="text-xs text-muted-foreground">
                    {loading ? "Loading…" : "No ticks recorded yet."}
                  </p>
                ) : (
                  <ul className="space-y-1">
                    {runs.slice(0, 8).map((r) => (
                      <li
                        key={r.id}
                        className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-background"
                      >
                        <span className="flex items-center gap-1.5 font-mono text-[11px] text-foreground/80">
                          <span
                            className={cn(
                              "inline-block size-1.5 rounded-full",
                              r.findings > 0 ? "bg-info" : "bg-muted-foreground/30",
                            )}
                            aria-hidden
                          />
                          <span suppressHydrationWarning>{clockTime(r.started_at)}</span>
                        </span>
                        <span className="text-[10px] text-muted-foreground">
                          {r.duration_ms}ms
                          {r.findings > 0 && (
                            <span className="ml-1.5 text-info">· {r.findings}</span>
                          )}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              {kindCounts.length > 0 && (
                <section>
                  <h2 className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                    Findings by kind
                  </h2>
                  <ul className="space-y-1.5">
                    {kindCounts.map(([kind, count]) => {
                      const Icon = FINDING_ICON[kind] ?? Brain;
                      const tone = FINDING_TONE[kind] ?? "text-foreground";
                      return (
                        <li
                          key={kind}
                          className="flex items-center gap-2 text-xs"
                        >
                          <Icon className={cn("size-3.5 shrink-0", tone.split(" ")[0])} aria-hidden />
                          <span className="flex-1 capitalize text-foreground">
                            {kind.replace("_", " ")}
                          </span>
                          <span className="font-mono text-[11px] text-muted-foreground">
                            {count}
                          </span>
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}

              {intents.length > 0 && (
                <section>
                  <h2 className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                    Intent split
                  </h2>
                  <IntentSplit intents={intents} />
                </section>
              )}
            </div>
          </aside>
        </div>
      </div>
    </TabFrame>
  );
}

function PulseIndicator({ alive }: { alive: boolean }) {
  return (
    <div className="relative flex size-12 shrink-0 items-center justify-center sm:size-14">
      {alive && (
        <>
          <span
            className="heartbeat-ring absolute inset-0 rounded-full bg-success/30"
            aria-hidden
          />
          <span
            className="heartbeat-ring-delay absolute inset-0 rounded-full bg-success/30"
            aria-hidden
          />
        </>
      )}
      <span
        className={cn(
          "relative flex size-6 items-center justify-center rounded-full sm:size-7",
          alive ? "bg-success" : "bg-muted-foreground/40",
        )}
      >
        <Activity
          className={cn(
            "size-3.5 text-background sm:size-4",
            !alive && "text-foreground/60",
          )}
          aria-hidden
        />
      </span>
    </div>
  );
}

function Stat({
  icon: Icon,
  value,
  label,
}: {
  icon: React.ComponentType<{ className?: string }>;
  value: number | string;
  label: string;
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <Icon className="size-3 text-muted-foreground" aria-hidden />
      <span className="font-mono font-medium text-foreground">{value}</span>
      <span>{label}</span>
    </span>
  );
}

function PulseRow({
  ev,
  onDiscuss,
  onDismiss,
}: {
  ev: PulseEvent;
  onDiscuss: (finding: HeartbeatFindingDTO) => void;
  onDismiss: (finding: HeartbeatFindingDTO) => void;
}) {
  if (ev.kind === "run" && ev.run) {
    const r = ev.run;
    const hasFindings = r.findings > 0;
    return (
      <li className="relative">
        <Dot tone={hasFindings ? "info" : "muted"} />
        <div className="rounded-lg border bg-card px-3 py-2">
          <div className="flex items-center gap-2 text-[11px]">
            <Activity className="size-3 text-muted-foreground" aria-hidden />
            <span className="font-mono text-foreground/80">
              <span suppressHydrationWarning>{clockTime(r.started_at)}</span>
            </span>
            <span className="text-muted-foreground">heartbeat tick</span>
            <span className="ml-auto font-mono text-muted-foreground">
              {r.duration_ms}ms
            </span>
          </div>
          {hasFindings ? (
            <p className="mt-1 text-xs text-foreground/80">
              {r.findings} {r.findings === 1 ? "finding" : "findings"} produced
            </p>
          ) : (
            <p className="mt-1 text-xs text-muted-foreground">no findings · all quiet</p>
          )}
        </div>
      </li>
    );
  }
  if (ev.kind === "finding" && ev.finding) {
    const f = ev.finding;
    const Icon = FINDING_ICON[f.kind] ?? Brain;
    const tone = FINDING_TONE[f.kind] ?? "text-foreground border-border";
    return (
      <li className="relative">
        <Dot tone={f.kind === "security" ? "danger" : f.kind === "surprise" ? "success" : "info"} />
        <div className={cn("rounded-lg border bg-card p-3", tone)}>
          <div className="flex items-center gap-2 text-[11px]">
            <Icon className="size-3.5" aria-hidden />
            <span className="font-mono uppercase tracking-wider">{f.kind.replace("_", " ")}</span>
            {f.pre_approved && (
              <Badge
                variant="outline"
                className="border-current/40 bg-background/60 font-mono text-[9px] uppercase"
              >
                pre-approved
              </Badge>
            )}
            <span className="ml-auto font-mono text-muted-foreground">
              <span suppressHydrationWarning>{clockTime(f.started_at)}</span>
            </span>
          </div>
          <p className="mt-1.5 text-sm font-medium text-foreground">{f.title}</p>
          {f.detail && (
            <p className="mt-1 break-words text-[13px] text-foreground/80">{f.detail}</p>
          )}
          {f.kind === "curiosity" && f.curiosity_id ? (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => onDiscuss(f)}
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-foreground px-3 text-xs font-medium text-background transition-colors hover:opacity-90"
              >
                <MessageSquare className="size-3.5" aria-hidden />
                Discuss
              </button>
              <button
                type="button"
                onClick={() => onDismiss(f)}
                className="inline-flex h-9 items-center rounded-md border bg-background px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                Dismiss
              </button>
            </div>
          ) : null}
        </div>
      </li>
    );
  }
  if (ev.kind === "intent" && ev.intent) {
    const i = ev.intent;
    const tone = INTENT_TONE[i.token];
    return (
      <li className="relative">
        <Dot
          tone={
            i.token === "full_assistance"
              ? "primary"
              : i.token === "fast_intervention"
                ? "info"
                : "muted"
          }
        />
        <div className="rounded-lg border bg-card p-3">
          <div className="flex items-center gap-2 text-[11px]">
            <MessageSquare className={cn("size-3", tone.cls)} aria-hidden />
            <span className={cn("font-mono font-semibold tracking-wider", tone.cls)}>
              {tone.label}
            </span>
            <span className="font-mono text-muted-foreground">
              {(i.confidence * 100).toFixed(0)}%
            </span>
            <span className="ml-auto font-mono text-muted-foreground">
              <span suppressHydrationWarning>{clockTime(i.created_at)}</span>
            </span>
          </div>
          {i.user_msg && (
            <p className="mt-1.5 line-clamp-2 break-words text-sm text-foreground">
              {i.user_msg}
            </p>
          )}
          {i.reason && (
            <p className="mt-1 text-[12px] italic text-muted-foreground">→ {i.reason}</p>
          )}
        </div>
      </li>
    );
  }
  return null;
}

function Dot({ tone }: { tone: "info" | "success" | "danger" | "primary" | "muted" }) {
  const cls = {
    info: "bg-info",
    success: "bg-success",
    danger: "bg-danger",
    primary: "bg-primary",
    muted: "bg-muted-foreground/40",
  }[tone];
  return (
    <span
      className={cn(
        "absolute -left-[21px] top-3 size-2.5 rounded-full ring-4 ring-background",
        cls,
      )}
      aria-hidden
    />
  );
}

function EmptyPulse({ loading, filter }: { loading: boolean; filter: EventFilter }) {
  if (loading) {
    return (
      <div className="rounded-xl border border-dashed bg-card/40 p-8 text-center text-sm text-muted-foreground">
        Loading the pulse stream…
      </div>
    );
  }
  const msg =
    filter === "findings"
      ? "No findings yet. Hit Pulse now or wait for the next tick — when the heartbeat notices something worth saying, it shows up here."
      : filter === "intent"
        ? "No classified turns yet. Every message you send through Live gets classified — they'll start landing here as you chat."
        : filter === "runs"
          ? "No heartbeat ticks recorded. Hit Pulse now to fire one manually."
          : "Quiet — no pulses yet. Send a message in Live or hit Pulse now to seed the stream.";
  return (
    <div className="rounded-xl border border-dashed bg-card/40 p-8 text-center">
      <Activity className="mx-auto mb-2 size-5 text-muted-foreground" aria-hidden />
      <p className="text-sm text-muted-foreground">{msg}</p>
    </div>
  );
}

function IntentSplit({ intents }: { intents: IntentRecordDTO[] }) {
  const total = intents.length;
  const counts = {
    full_assistance: intents.filter((i) => i.token === "full_assistance").length,
    fast_intervention: intents.filter((i) => i.token === "fast_intervention").length,
    silent: intents.filter((i) => i.token === "silent").length,
  };
  return (
    <div className="space-y-2">
      {(Object.keys(counts) as (keyof typeof counts)[]).map((k) => {
        const c = counts[k];
        const pct = total > 0 ? (c / total) * 100 : 0;
        const tone = INTENT_TONE[k];
        return (
          <div key={k} className="space-y-1">
            <div className="flex items-center justify-between text-[11px]">
              <span className={cn("font-mono font-semibold tracking-wider", tone.cls)}>
                {tone.label}
              </span>
              <span className="font-mono text-muted-foreground">
                {c} · {pct.toFixed(0)}%
              </span>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-muted">
              <div
                className={cn(
                  "h-full rounded-full transition-all",
                  k === "full_assistance" && "bg-primary",
                  k === "fast_intervention" && "bg-info",
                  k === "silent" && "bg-muted-foreground/40",
                )}
                style={{ width: `${pct}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}
