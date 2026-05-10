"use client";

import { useEffect, useState } from "react";
import { IconRefresh, IconHistory, IconClock } from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { fetchSessions, type SessionDTO } from "@/lib/api";

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

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <aside className="flex min-h-0 flex-col border-b bg-background lg:w-80 lg:border-b-0 lg:border-r">
          <div className="flex items-center justify-between gap-2 px-3 py-3">
            <h2 className="text-xs uppercase tracking-wide text-muted-foreground">
              In-memory sessions
            </h2>
            <Button size="icon" variant="ghost" onClick={refresh} aria-label="Refresh sessions">
              <IconRefresh className="size-4" />
            </Button>
          </div>
          <div className="flex max-h-72 flex-col gap-2 overflow-y-auto px-3 pb-3 scroll-touch lg:max-h-none lg:flex-1">
            {sessions.length === 0 ? (
              <p className="px-1 text-sm text-muted-foreground">
                {loading
                  ? "Loading…"
                  : "No active sessions. Phase 4 will surface persisted sessions from the database."}
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
                      <IconClock className="size-3" aria-hidden />
                      <time suppressHydrationWarning>{new Date(s.started_at).toLocaleString()}</time>
                    </span>
                  </div>
                  <div className="mt-1 flex items-center gap-2 text-sm">
                    <IconHistory className="size-4 text-muted-foreground" aria-hidden />
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
                  Pick a session to view metadata. Full transcript / observations / memories-created
                  sub-tabs land in Phase 4 once persistence is wired through every layer.
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
                    The transcript replay and observations log will be wired here once we persist
                    every turn into <code className="font-mono">mem_observations</code>. The Memory tab
                    already shows the rolling capture for any DB-backed deployment.
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
