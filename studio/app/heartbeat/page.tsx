"use client";

import { useEffect, useState } from "react";
import { Activity, RefreshCw, Play } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { SectionCard } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  PageSectionHeader,
  HeaderAction,
} from "@/components/ui/page-tabs";
import {
  fetchHeartbeats,
  runHeartbeatNow,
  type HeartbeatRunDTO,
  type HeartbeatRunSummaryDTO,
} from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

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

const findingTone: Record<string, string> = {
  outcome: "border-warning/40 bg-warning/10 text-warning",
  pattern: "border-info/40 bg-info/10 text-info",
  curiosity: "border-info/40 bg-info/10 text-info",
  surprise: "border-success/40 bg-success/10 text-success",
  security: "border-danger/40 bg-danger/10 text-danger",
  self_heal: "border-orange-500/40 bg-orange-500/10 text-orange-500",
};

function formatInterval(seconds: number) {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

export default function HeartbeatPage() {
  const [interval, setInterval] = useState(0);
  const [runs, setRuns] = useState<HeartbeatRunDTO[]>([]);
  const [running, setRunning] = useState(false);
  const [last, setLast] = useState<HeartbeatRunSummaryDTO | null>(null);
  const [loading, setLoading] = useState(true);

  async function load() {
    setLoading(true);
    const r = await fetchHeartbeats();
    if (r) {
      setInterval(r.interval_seconds);
      setRuns(r.runs);
    }
    setLoading(false);
  }

  useEffect(() => {
    load();
  }, []);

  useRealtime(["mem_heartbeats", "mem_heartbeat_findings", "mem_intent_decisions"], load);

  async function fireNow() {
    setRunning(true);
    const res = await runHeartbeatNow();
    setRunning(false);
    if (res) setLast(res);
    await load();
  }

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="border-b px-3 py-3 sm:px-4">
          <SectionCard>
            <PageSectionHeader
                title="heartbeat"
                meta={
                  <Badge variant="outline" className="font-mono text-[10px]">
                    every {interval ? formatInterval(interval) : "—"}
                  </Badge>
                }
              >
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
                  onClick={load}
                  disabled={loading}
                />
              </PageSectionHeader>

              <div className="flex items-baseline gap-2">
                <span className="font-sans text-5xl font-semibold leading-none tabular-nums tracking-tight text-foreground">
                  {runs.length}
                </span>
                <span className="text-xs uppercase tracking-wider text-muted-foreground">
                  {runs.length === 1 ? "run" : "runs"}
                </span>
                {runs[0] ? (
                  <span
                    className="ml-auto text-[11px] text-muted-foreground"
                    suppressHydrationWarning
                  >
                    last {relTime(runs[0].started_at)}
                  </span>
                ) : null}
              </div>
          </SectionCard>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
          {last && (
            <section className="mb-4 space-y-2">
              <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Last run findings ({last.findings.length})
              </div>
              {last.findings.length === 0 ? (
                <p className="text-sm text-muted-foreground">No findings.</p>
              ) : (
                <ul className="space-y-2">
                  {last.findings.map((f, i) => (
                    <li
                      key={i}
                      className={`rounded-md border p-3 text-sm ${findingTone[f.kind] ?? "border-muted"}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <strong>{f.title}</strong>
                        <Badge variant="outline" className="font-mono uppercase">
                          {f.kind}
                        </Badge>
                      </div>
                      {f.detail && <p className="mt-1 break-words">{f.detail}</p>}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}

          <section className="space-y-2">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
              Recent runs
            </div>
            {runs.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {loading ? "Loading…" : "No runs yet — hit Run heartbeat now."}
              </p>
            ) : (
              <ul className="space-y-2">
                {runs.map((r) => (
                  <li key={r.id} className="rounded-xl border bg-card px-3 py-2">
                    <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="flex items-center gap-1.5">
                        <Activity className="size-3" aria-hidden />
                        <time suppressHydrationWarning>
                          {new Date(r.started_at).toLocaleString()}
                        </time>
                      </span>
                      <span className="font-mono">{r.duration_ms}ms · {r.findings} findings</span>
                    </div>
                    {r.summary && (
                      <pre className="mt-1 whitespace-pre-wrap break-words text-xs">{r.summary}</pre>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      </div>
    </TabFrame>
  );
}
