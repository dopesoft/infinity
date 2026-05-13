"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  Brain,
  Eye,
  Lightbulb,
  Play,
  RefreshCw,
  Search,
  Shield,
  Sparkles,
  Wrench,
  Zap,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Badge } from "@/components/ui/badge";
import { MetricCard } from "@/components/MetricCard";
import {
  PageTabs,
  PageTabsList,
  PageTabsTrigger,
  HScrollRow,
  FilterPill,
  PageSectionHeader,
  HeaderAction,
} from "@/components/ui/page-tabs";
import { cn } from "@/lib/utils";
import {
  fetchHeartbeats,
  fetchHeartbeatFindings,
  fetchIntentRecent,
  runHeartbeatNow,
  type HeartbeatRunDTO,
  type HeartbeatRunSummaryDTO,
  type HeartbeatFindingDTO,
  type IntentRecordDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

/* Heartbeat tab redesign. Mirrors the visual grammar of /memory:
 *
 *   - top metric strip (snap-scroll on mobile, grid on sm+)
 *   - PageTabs sub-view switcher (runs · findings · intent)
 *   - HScrollRow filter pills under findings + intent for kind/token filtering
 *   - shared HeaderAction buttons in a sticky section header
 *
 * The page is the source of truth for diagnostic surfaces on the proactive
 * engine: every heartbeat tick, every finding produced (so the boss can audit
 * what the agent decided was worth saying first), and every IntentFlow
 * classification (so the boss can audit how the agent triaged each turn).
 * No realtime cuts — useRealtime re-loads the active sub-view's data when the
 * underlying table fires a row change. */

type View = "runs" | "findings" | "intent";
const VIEWS: { key: View; label: string }[] = [
  { key: "runs", label: "runs" },
  { key: "findings", label: "findings" },
  { key: "intent", label: "intent" },
];

const FINDING_KIND_FILTERS = [
  "all",
  "surprise",
  "curiosity",
  "security",
  "outcome",
  "pattern",
  "self_heal",
] as const;
type FindingKindFilter = (typeof FINDING_KIND_FILTERS)[number];

const INTENT_TOKEN_FILTERS = [
  "all",
  "full_assistance",
  "fast_intervention",
  "silent",
] as const;
type IntentTokenFilter = (typeof INTENT_TOKEN_FILTERS)[number];

const FINDING_TONE: Record<string, string> = {
  outcome: "border-warning/40 bg-warning/10 text-warning",
  pattern: "border-info/40 bg-info/10 text-info",
  curiosity: "border-info/40 bg-info/10 text-info",
  surprise: "border-success/40 bg-success/10 text-success",
  security: "border-danger/40 bg-danger/10 text-danger",
  self_heal: "border-orange-500/40 bg-orange-500/10 text-orange-500",
};

const FINDING_ICON: Record<string, React.ComponentType<{ className?: string }>> = {
  surprise: Sparkles,
  curiosity: Lightbulb,
  security: Shield,
  outcome: AlertTriangle,
  pattern: Eye,
  self_heal: Wrench,
};

const INTENT_TONE: Record<IntentRecordDTO["token"], { label: string; cls: string }> = {
  fast_intervention: {
    label: "FAST",
    cls: "bg-info/15 text-info border-info/40",
  },
  silent: {
    label: "SILENT",
    cls: "bg-muted text-muted-foreground border-muted-foreground/20",
  },
  full_assistance: {
    label: "FULL",
    cls: "bg-primary/15 text-primary border-primary/40",
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

function formatInterval(seconds: number) {
  if (seconds <= 0) return "—";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

export default function HeartbeatPage() {
  const [intervalSeconds, setIntervalSeconds] = useState(0);
  const [runs, setRuns] = useState<HeartbeatRunDTO[]>([]);
  const [findings, setFindings] = useState<HeartbeatFindingDTO[]>([]);
  const [intents, setIntents] = useState<IntentRecordDTO[]>([]);
  const [running, setRunning] = useState(false);
  const [last, setLast] = useState<HeartbeatRunSummaryDTO | null>(null);
  const [loading, setLoading] = useState(true);

  const [view, setView] = useState<View>("runs");
  const [findingKind, setFindingKind] = useState<FindingKindFilter>("all");
  const [intentToken, setIntentToken] = useState<IntentTokenFilter>("all");
  const [search, setSearch] = useState("");

  async function loadRuns() {
    const r = await fetchHeartbeats();
    if (r) {
      setIntervalSeconds(r.interval_seconds);
      setRuns(r.runs);
    }
  }
  async function loadFindings(kind: FindingKindFilter = findingKind) {
    const f = await fetchHeartbeatFindings(100, kind);
    setFindings(f ?? []);
  }
  async function loadIntents() {
    const i = await fetchIntentRecent(100);
    setIntents(i ?? []);
  }

  async function loadAll() {
    setLoading(true);
    await Promise.all([loadRuns(), loadFindings(), loadIntents()]);
    setLoading(false);
  }

  useEffect(() => {
    loadAll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useRealtime(["mem_heartbeats", "mem_heartbeat_findings", "mem_intent_decisions"], () => {
    /* Realtime push from any of the three underlying tables — refresh the
     * active sub-view's dataset only (avoid hammering all three endpoints
     * on every row change). The other views stay stale until the user
     * switches; they re-fetch in the view's effect below. */
    if (view === "runs") loadRuns();
    else if (view === "findings") loadFindings();
    else loadIntents();
  });

  /* Per-view re-fetch when filters change. The lists are short enough that
   * client-side filtering would be simpler but server-side keeps the limit
   * behaviour honest — kind=surprise returns the last 100 surprises, not
   * 100 random findings filtered down to a handful. */
  useEffect(() => {
    if (view === "findings") loadFindings(findingKind);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [findingKind, view]);

  async function fireNow() {
    setRunning(true);
    const res = await runHeartbeatNow();
    setRunning(false);
    if (res) setLast(res);
    await loadAll();
  }

  const filteredIntents = useMemo(() => {
    let list = intents;
    if (intentToken !== "all") {
      list = list.filter((i) => i.token === intentToken);
    }
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter(
        (i) =>
          i.user_msg.toLowerCase().includes(q) ||
          (i.reason ?? "").toLowerCase().includes(q),
      );
    }
    return list;
  }, [intents, intentToken, search]);

  const filteredFindings = useMemo(() => {
    if (!search.trim()) return findings;
    const q = search.trim().toLowerCase();
    return findings.filter(
      (f) =>
        f.title.toLowerCase().includes(q) ||
        (f.detail ?? "").toLowerCase().includes(q),
    );
  }, [findings, search]);

  const lastRun = runs[0];

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          {/* Metric strip — mobile snap-scroll, sm+ grid. Mirrors /memory so
              the two pages read as the same family. */}
          <div className="-mx-3 sm:mx-0">
            <div className="no-scrollbar flex snap-x snap-mandatory gap-2 overflow-x-auto scroll-touch px-3 pb-1 sm:grid sm:grid-cols-4 sm:gap-2 sm:overflow-visible sm:px-0 sm:pb-0">
              <MetricCard
                label="runs"
                value={runs.length}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="findings"
                value={findings.length}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="intent decisions"
                value={intents.length}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
              <MetricCard
                label="interval"
                value={formatInterval(intervalSeconds)}
                className="min-w-[10.5rem] shrink-0 snap-start sm:min-w-0"
              />
            </div>
          </div>

          {/* Action header — Run + Refresh. Last-run timestamp on the right. */}
          <div className="mx-auto flex w-full items-center gap-2 sm:max-w-3xl">
            <div className="flex flex-1 items-baseline gap-2">
              <Activity className="size-4 text-muted-foreground" aria-hidden />
              <span className="text-sm font-medium tracking-tight text-foreground">
                heartbeat
              </span>
              {lastRun ? (
                <span
                  className="text-[11px] text-muted-foreground"
                  suppressHydrationWarning
                >
                  · last {relTime(lastRun.started_at)}
                </span>
              ) : null}
            </div>
            <HeaderAction
              icon={<Play className="size-4" />}
              label={running ? "Running…" : "Run now"}
              primary
              onClick={fireNow}
              disabled={running}
              loading={running}
            />
            <HeaderAction
              icon={<RefreshCw className="size-4" />}
              label="Refresh"
              onClick={loadAll}
              disabled={loading}
            />
          </div>

          {/* Sub-view switcher */}
          <div className="space-y-3">
            <PageTabs
              value={view}
              onValueChange={(v) => setView(v as View)}
              className="w-full"
            >
              <PageTabsList columns={3}>
                {VIEWS.map(({ key, label }) => {
                  const count =
                    key === "runs"
                      ? runs.length
                      : key === "findings"
                        ? findings.length
                        : intents.length;
                  return (
                    <PageTabsTrigger key={key} value={key} className="gap-1.5">
                      <span>{label}</span>
                      <span
                        className={cn(
                          "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                          view === key
                            ? "bg-foreground text-background"
                            : "bg-muted-foreground/15 text-muted-foreground",
                        )}
                        aria-label={`${count} total`}
                      >
                        {count}
                      </span>
                    </PageTabsTrigger>
                  );
                })}
              </PageTabsList>
            </PageTabs>

            {view === "findings" && (
              <HScrollRow>
                {FINDING_KIND_FILTERS.map((k) => (
                  <FilterPill
                    key={k}
                    active={findingKind === k}
                    onClick={() => setFindingKind(k)}
                  >
                    {k}
                  </FilterPill>
                ))}
              </HScrollRow>
            )}
            {view === "intent" && (
              <HScrollRow>
                {INTENT_TOKEN_FILTERS.map((t) => (
                  <FilterPill
                    key={t}
                    active={intentToken === t}
                    onClick={() => setIntentToken(t)}
                  >
                    {t === "all" ? "all" : INTENT_TONE[t as keyof typeof INTENT_TONE].label.toLowerCase()}
                  </FilterPill>
                ))}
              </HScrollRow>
            )}

            {(view === "findings" || view === "intent") && (
              <div className="relative mx-auto w-full sm:max-w-2xl">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <input
                  type="search"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder={
                    view === "findings"
                      ? "Search findings…"
                      : "Search classified turns…"
                  }
                  inputMode="search"
                  className="flex h-9 w-full rounded-md border border-input bg-background pl-9 pr-3 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
              </div>
            )}
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
          {view === "runs" && (
            <RunsList runs={runs} loading={loading} last={last} />
          )}
          {view === "findings" && (
            <FindingsList findings={filteredFindings} loading={loading} />
          )}
          {view === "intent" && (
            <IntentList intents={filteredIntents} loading={loading} />
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function RunsList({
  runs,
  loading,
  last,
}: {
  runs: HeartbeatRunDTO[];
  loading: boolean;
  last: HeartbeatRunSummaryDTO | null;
}) {
  return (
    <div className="mx-auto w-full max-w-3xl space-y-4">
      {last && last.findings.length > 0 && (
        <section className="space-y-2">
          <PageSectionHeader
            title="last run · live findings"
            count={last.findings.length}
            className="px-0 pb-1 pt-0"
          />
          <ul className="space-y-2">
            {last.findings.map((f, i) => (
              <FindingCard
                key={`live-${i}`}
                kind={f.kind}
                title={f.title}
                detail={f.detail}
                pre_approved={f.pre_approved}
              />
            ))}
          </ul>
        </section>
      )}

      <section className="space-y-2">
        <PageSectionHeader
          title="recent runs"
          count={runs.length}
          className="px-0 pb-1 pt-0"
        />
        {runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {loading ? "Loading…" : "No runs yet — hit Run now to fire the heartbeat."}
          </p>
        ) : (
          <ul className="space-y-2">
            {runs.map((r) => (
              <li
                key={r.id}
                className="rounded-xl border bg-card px-3 py-2.5"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <Activity className="size-3" aria-hidden />
                    <time suppressHydrationWarning>
                      {new Date(r.started_at).toLocaleString()}
                    </time>
                  </span>
                  <span className="flex items-center gap-2">
                    <Badge variant="outline" className="font-mono text-[10px]">
                      {r.duration_ms}ms
                    </Badge>
                    <Badge
                      variant="outline"
                      className={cn(
                        "font-mono text-[10px]",
                        r.findings > 0 && "border-info/40 text-info",
                      )}
                    >
                      {r.findings} {r.findings === 1 ? "finding" : "findings"}
                    </Badge>
                  </span>
                </div>
                {r.summary && r.summary !== "no findings" && (
                  <pre className="mt-1.5 whitespace-pre-wrap break-words text-[12px] text-foreground/80">
                    {r.summary}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function FindingsList({
  findings,
  loading,
}: {
  findings: HeartbeatFindingDTO[];
  loading: boolean;
}) {
  return (
    <div className="mx-auto w-full max-w-3xl space-y-2">
      {findings.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {loading
            ? "Loading…"
            : "No findings yet. Findings land here every time the heartbeat ticks and notices something."}
        </p>
      ) : (
        <ul className="space-y-2">
          {findings.map((f) => (
            <FindingCard
              key={f.id}
              kind={f.kind}
              title={f.title}
              detail={f.detail}
              pre_approved={f.pre_approved}
              when={f.started_at}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function FindingCard({
  kind,
  title,
  detail,
  pre_approved,
  when,
}: {
  kind: string;
  title: string;
  detail?: string;
  pre_approved?: boolean;
  when?: string;
}) {
  const Icon = FINDING_ICON[kind] ?? Brain;
  const tone = FINDING_TONE[kind] ?? "border-muted bg-card";
  return (
    <li
      className={cn(
        "flex gap-3 rounded-xl border p-3 text-sm",
        tone,
      )}
    >
      <Icon className="mt-0.5 size-4 shrink-0 opacity-80" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="break-words text-foreground">{title}</strong>
          <Badge
            variant="outline"
            className="border-current/30 bg-background/60 font-mono text-[10px] uppercase"
          >
            {kind}
          </Badge>
          {pre_approved && (
            <Badge
              variant="outline"
              className="border-success/40 bg-success/10 font-mono text-[10px] uppercase text-success"
            >
              pre-approved
            </Badge>
          )}
          {when && (
            <span
              className="ml-auto text-[11px] text-muted-foreground"
              suppressHydrationWarning
            >
              {relTime(when)}
            </span>
          )}
        </div>
        {detail && (
          <p className="mt-1 break-words text-foreground/85">{detail}</p>
        )}
      </div>
    </li>
  );
}

function IntentList({
  intents,
  loading,
}: {
  intents: IntentRecordDTO[];
  loading: boolean;
}) {
  return (
    <div className="mx-auto w-full max-w-3xl space-y-2">
      {intents.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {loading
            ? "Loading…"
            : "No classifications yet. Every message you send gets classified — they'll start landing here as you chat."}
        </p>
      ) : (
        <ul className="space-y-2">
          {intents.map((r) => {
            const tone = INTENT_TONE[r.token];
            return (
              <li
                key={r.id}
                className="rounded-xl border bg-card px-3 py-2.5"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    variant="outline"
                    className={cn(
                      "font-mono text-[10px] font-semibold tracking-wider",
                      tone.cls,
                    )}
                  >
                    <Zap className="mr-1 size-3" aria-hidden />
                    {tone.label}
                  </Badge>
                  <span className="font-mono text-[11px] text-muted-foreground">
                    {(r.confidence * 100).toFixed(0)}%
                  </span>
                  <span
                    className="ml-auto text-[11px] text-muted-foreground"
                    suppressHydrationWarning
                  >
                    {relTime(r.created_at)}
                  </span>
                </div>
                <p className="mt-1.5 line-clamp-2 break-words text-[13px] text-foreground">
                  {r.user_msg || "—"}
                </p>
                {r.reason && (
                  <p className="mt-1 text-[12px] italic text-muted-foreground">
                    → {r.reason}
                  </p>
                )}
                {r.suggested_action && (
                  <p className="mt-1 text-[12px] text-foreground/85">
                    <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
                      suggested:
                    </span>{" "}
                    {r.suggested_action}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
