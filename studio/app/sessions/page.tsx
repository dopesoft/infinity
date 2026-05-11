"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { RefreshCw, History, Clock, Play, Loader2 } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PageSectionHeader, HeaderAction } from "@/components/ui/page-tabs";
import { fetchSessions, fetchSessionMessages, type SessionDTO, type SessionMessageDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { cn } from "@/lib/utils";

const SESSION_KEY = "infinity:sessionId";
const MESSAGES_KEY_PREFIX = "infinity:messages:";

function primeLiveCache(sessionId: string, messages: SessionMessageDTO[]) {
  if (typeof window === "undefined") return;
  try {
    const shaped = messages.map((m, i) => ({
      id: `${sessionId}-${i}`,
      role: m.role,
      text: m.text,
      createdAt: Date.parse(m.created_at) || Date.now(),
    }));
    window.localStorage.setItem(SESSION_KEY, sessionId);
    window.localStorage.setItem(MESSAGES_KEY_PREFIX + sessionId, JSON.stringify(shaped));
  } catch {
    /* private mode / quota — Live will rehydrate from Core anyway */
  }
}

export default function SessionsPage() {
  const [sessions, setSessions] = useState<SessionDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<SessionDTO | null>(null);
  const [messages, setMessages] = useState<SessionMessageDTO[]>([]);
  const [messagesLoading, setMessagesLoading] = useState(false);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const router = useRouter();

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

  useEffect(() => {
    if (!selected) {
      setMessages([]);
      return;
    }
    const ctrl = new AbortController();
    setMessagesLoading(true);
    fetchSessionMessages(selected.id, ctrl.signal)
      .then((rows) => {
        setMessages(rows ?? []);
        setMessagesLoading(false);
        requestAnimationFrame(() => {
          transcriptRef.current?.scrollTo({ top: transcriptRef.current.scrollHeight });
        });
      })
      .catch(() => setMessagesLoading(false));
    return () => ctrl.abort();
  }, [selected?.id]);

  function resume() {
    if (!selected) return;
    primeLiveCache(selected.id, messages);
    router.push("/live");
  }

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <aside className="flex min-h-0 flex-col border-b bg-background lg:w-80 lg:border-b-0 lg:border-r">
          <PageSectionHeader
            title="sessions"
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
                {loading ? "Loading…" : "No sessions yet — start a chat in Live."}
              </p>
            ) : (
              sessions.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => setSelected(s)}
                  className={cn(
                    "rounded-xl border bg-card px-3 py-2 text-left transition-colors hover:bg-accent",
                    selected?.id === s.id ? "border-info ring-1 ring-info" : "",
                  )}
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
                  Pick a session to view its transcript and pick the thread back up in Live.
                </CardDescription>
              </CardContent>
            </Card>
          ) : (
            <div className="flex min-h-0 flex-1 flex-col gap-3">
              <div className="rounded-xl border bg-card p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                      session
                    </div>
                    <code className="mt-1 block break-all font-mono text-sm">{selected.id}</code>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <Badge variant="outline" suppressHydrationWarning>
                        started {new Date(selected.started_at).toLocaleString()}
                      </Badge>
                      <Badge variant="secondary">{selected.message_count} messages</Badge>
                    </div>
                  </div>
                  <Button
                    type="button"
                    onClick={resume}
                    disabled={messagesLoading}
                    className="shrink-0"
                  >
                    <Play className="size-4" aria-hidden />
                    Resume in Live
                  </Button>
                </div>
              </div>

              <div
                ref={transcriptRef}
                className="min-h-0 flex-1 overflow-y-auto rounded-xl border bg-card p-3 scroll-touch sm:p-4"
              >
                {messagesLoading ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    <Loader2 className="mr-2 size-4 animate-spin" aria-hidden />
                    Loading transcript…
                  </div>
                ) : messages.length === 0 ? (
                  <p className="px-1 text-sm text-muted-foreground">
                    No user/assistant turns captured for this session. Tool calls and
                    other observations are visible in the Memory tab.
                  </p>
                ) : (
                  <div className="flex flex-col gap-3">
                    {messages.map((m, i) => (
                      <div
                        key={i}
                        className={cn(
                          "flex w-full",
                          m.role === "user" ? "justify-end" : "justify-start",
                        )}
                      >
                        <div
                          className={cn(
                            "rounded-2xl px-3 py-2 text-[15px] leading-relaxed sm:text-base",
                            m.role === "user"
                              ? "max-w-[88%] rounded-tr-sm bg-primary text-primary-foreground sm:max-w-[78%]"
                              : "max-w-full rounded-tl-sm bg-muted text-foreground sm:max-w-[75%]",
                          )}
                        >
                          <div className="whitespace-pre-wrap break-words">{m.text}</div>
                          <div
                            className={cn(
                              "mt-1 text-[11px]",
                              m.role === "user"
                                ? "text-primary-foreground/70"
                                : "text-muted-foreground",
                            )}
                          >
                            <time suppressHydrationWarning>
                              {new Date(m.created_at).toLocaleString()}
                            </time>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </section>
      </div>
    </TabFrame>
  );
}
