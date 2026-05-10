"use client";

import { useEffect, useState } from "react";
import { IconActivity, IconRefresh, IconPlayerPlay } from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  fetchHeartbeats,
  runHeartbeatNow,
  type HeartbeatRunDTO,
  type HeartbeatRunSummaryDTO,
} from "@/lib/api";

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
          <Card>
            <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="space-y-1">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  heartbeat
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline" className="font-mono">
                    every {interval ? formatInterval(interval) : "—"}
                  </Badge>
                  <Badge variant="secondary" className="font-mono">
                    {runs.length} runs
                  </Badge>
                  {runs[0] && (
                    <Badge variant="outline" className="font-mono" suppressHydrationWarning>
                      last {new Date(runs[0].started_at).toLocaleString()}
                    </Badge>
                  )}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Button onClick={fireNow} disabled={running}>
                  <IconPlayerPlay className="mr-1 size-4" />
                  {running ? "running…" : "Run heartbeat now"}
                </Button>
                <Button size="icon" variant="ghost" onClick={load} aria-label="Refresh" disabled={loading}>
                  <IconRefresh className="size-4" />
                </Button>
              </div>
            </CardContent>
          </Card>
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
                        <IconActivity className="size-3" aria-hidden />
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
