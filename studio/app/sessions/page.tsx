"use client";

import { useEffect, useState } from "react";
import { RefreshCw, History, Clock } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { PageSectionHeader, HeaderAction } from "@/components/ui/page-tabs";
import { fetchSessions, type SessionDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<SessionDTO | null>(null);

  async function refresh() {
    setLoading(true);
    const list = await fetchSessions();
    setSessions(list ?? []);
    setLoading(false);
    if (list && list.length > 0 && !selected) {
      setSelected(list[0]);
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  useRealtime("mem_sessions", refresh);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <aside className="flex min-h-0 flex-col border-b bg-background lg:w-80 lg:border-b-0 lg:border-r">
          <PageSectionHeader
            title="in-memory sessions"
            count={sessions.length}
            className="px-3 py-3"
          >
            <HeaderAction
              icon={<RefreshCw className="size-4" />}
              label="Refresh"
              onClick={refresh}
            />
          </PageSectionHeader>
          <div className="flex max-h-72 flex-col gap-2 overflow-y-auto px-3 pb-3 scroll-touch lg:max-h-none lg:flex-1">
            {sessions.length === 0 ? (
              <p className="px-1 text-sm text-muted-foreground">
                {loading ? "Loading…" : "No active sessions."}
              </p>
            ) : (
              sessions.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => setSelected(s)}
                  className={`rounded-xl border bg-card px-3 py-2 text-left transition-colors hover:bg-accent ${
                    selected?.id === s.id ? "border-info ring-1 ring-info" : ""
                  }`}
                >
                  <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                    <span className="font-mono">{s.id.slice(0, 12)}…</span>
                    <span className="flex items-center gap-1">
                      <Clock className="size-3" aria-hidden />
                      <time suppressHydrationWarning>{new Date(s.started_at).toLocaleString()}</time>
                    </span>
                  </div>
                  <div className="mt-1 flex items-center gap-2 text-sm">
                    <History className="size-4 text-muted-foreground" aria-hidden />
                    <span>{s.message_count} messages</span>
                  </div>
                </button>
              ))
            )}
          </div>
        </aside>

        <section className="flex min-h-0 flex-1 flex-col p-3 sm:p-4">
          {!selected ? (
            <Card className="m-auto max-w-md">
              <CardContent className="space-y-2 p-6 text-center">
                <CardTitle>Sessions</CardTitle>
                <CardDescription>
                  Pick a session to view its transcript, observations, and the memories it produced.
                </CardDescription>
              </CardContent>
            </Card>
          ) : (
            <div className="space-y-3">
              <div className="rounded-xl border bg-card p-4">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  session
                </div>
                <code className="mt-1 block break-all font-mono text-sm">{selected.id}</code>
                <div className="mt-3 flex flex-wrap gap-2">
                  <Badge variant="outline" suppressHydrationWarning>started {new Date(selected.started_at).toLocaleString()}</Badge>
                  <Badge variant="secondary">{selected.message_count} messages</Badge>
                </div>
              </div>
              <Card>
                <CardContent className="space-y-2 p-4 text-sm text-muted-foreground">
                  <p>
                    Open the Memory tab to see every observation captured during this session.
                  </p>
                </CardContent>
              </Card>
            </div>
          )}
        </section>
      </div>
    </TabFrame>
  );
}
