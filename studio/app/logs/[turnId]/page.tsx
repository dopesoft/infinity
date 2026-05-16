"use client";

import { use, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { ArrowLeft, RefreshCw } from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TurnStatusPip } from "@/components/logs/TurnStatusPip";
import { TraceTimeline } from "@/components/logs/TraceTimeline";
import { TraceEventDetail } from "@/components/logs/TraceEventDetail";
import { cn } from "@/lib/utils";
import { fetchTraceDetail, type TraceDetailDTO, type TraceEventDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";

/* /logs/[turnId] — full timeline + per-event detail.
 *
 * Header convention matches /memory + /skills exactly: a `space-y-3 border-b
 * px-3 py-3 sm:px-4` strip on top of TabFrame, list area below with the
 * same px-3/sm:px-4 padding. Mobile (<lg): timeline stacks above the
 * detail pane. Desktop: timeline rail sticky on the left.
 */
export default function LogDetailPage({ params }: { params: Promise<{ turnId: string }> }) {
  const { turnId } = use(params);
  const router = useRouter();
  const [detail, setDetail] = useState<TraceDetailDTO | null>(null);
  const [selected, setSelected] = useState<TraceEventDTO | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    const r = await fetchTraceDetail(turnId);
    setDetail(r);
    setSelected((prev) => {
      if (!r) return null;
      if (prev && r.events.some((e) => e.id === prev.id)) return prev;
      return r.events[0] ?? null;
    });
    setLoading(false);
  }, [turnId]);

  useEffect(() => {
    void load();
  }, [load]);

  const isInFlight = detail?.turn.status === "in_flight";
  useRealtime(
    ["mem_observations", "mem_predictions", "mem_trust_contracts", "mem_turns"],
    () => {
      if (isInFlight) void load();
    },
  );

  const turn = detail?.turn;
  const events = detail?.events ?? [];

  const tokenSummary = useMemo(() => {
    if (!turn) return "";
    if (!turn.input_tokens && !turn.output_tokens) return "";
    return `${turn.input_tokens.toLocaleString()} → ${turn.output_tokens.toLocaleString()}`;
  }, [turn]);

  const latencyLabel = useMemo(() => {
    if (!turn?.latency_ms) return "";
    const s = turn.latency_ms / 1000;
    if (s < 1) return `${Math.round(turn.latency_ms)}ms`;
    if (s < 60) return `${s.toFixed(1)}s`;
    return `${Math.round(s / 60)}m ${Math.round(s % 60)}s`;
  }, [turn]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        {/* Header strip — same px-3 py-3 sm:px-4 rhythm as every other page. */}
        <div className="space-y-2 border-b px-3 py-3 sm:px-4">
          <div className="flex min-w-0 items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => router.push("/logs")}
              className="h-8 shrink-0 gap-1.5 px-2 text-muted-foreground hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              <span className="hidden sm:inline">Logs</span>
            </Button>
            <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
              turn
            </span>
            {turn && (
              <>
                <TurnStatusPip status={turn.status} />
                <Badge variant="secondary" className="font-mono text-[10px] uppercase">
                  {turn.status}
                </Badge>
              </>
            )}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void load()}
              disabled={loading}
              aria-label="Refresh"
              title="Refresh"
              className="ml-auto h-8 w-8 shrink-0 px-0 text-muted-foreground hover:text-foreground"
            >
              <RefreshCw className={cn("size-4", loading && "animate-spin")} aria-hidden />
            </Button>
          </div>

          {turn && (
            <div className="space-y-1">
              <h1 className="line-clamp-2 break-words text-sm font-semibold text-foreground sm:text-base">
                {turn.user_text || (
                  <span className="text-muted-foreground">(resumed turn — no fresh prompt)</span>
                )}
              </h1>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-muted-foreground">
                {turn.model && <span className="break-all">{turn.model}</span>}
                {turn.session_name && <span className="truncate">· {turn.session_name}</span>}
                {turn.tool_call_count > 0 && (
                  <span>
                    · {turn.tool_call_count} {turn.tool_call_count === 1 ? "tool" : "tools"}
                  </span>
                )}
                {tokenSummary && <span>· {tokenSummary} tok</span>}
                {latencyLabel && <span>· {latencyLabel}</span>}
                {turn.stop_reason && <span>· {turn.stop_reason}</span>}
              </div>
              {turn.error && (
                <div className="rounded-md border border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger break-words">
                  {turn.error}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Body — same px-3 py-3 sm:px-4 rhythm as the list page. */}
        <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-3 py-3 scroll-touch sm:px-4">
          {!detail && !loading && (
            <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
              Turn not found.
            </div>
          )}

          {detail && (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-[240px_minmax(0,1fr)]">
              <aside className="min-w-0 lg:sticky lg:top-3 lg:max-h-[calc(100dvh-160px)] lg:overflow-y-auto">
                <div className="mb-2 flex items-center gap-2">
                  <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                    timeline
                  </span>
                  <Badge
                    variant="secondary"
                    className="h-5 min-w-[1.25rem] justify-center px-1.5 font-mono text-[10px]"
                  >
                    {events.length}
                  </Badge>
                </div>
                <TraceTimeline
                  events={events}
                  selectedId={selected?.id ?? null}
                  onSelect={setSelected}
                />
              </aside>
              <section className="min-w-0">
                <TraceEventDetail event={selected} />
                {turn?.assistant_text && selected?.kind !== "assistant" && (
                  <div className="mt-4">
                    <div className="mb-2 flex items-center gap-2">
                      <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                        final reply
                      </span>
                    </div>
                    <pre className="max-h-[40vh] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-relaxed text-foreground">
                      {turn.assistant_text}
                    </pre>
                  </div>
                )}
              </section>
            </div>
          )}
        </div>
      </div>
    </TabFrame>
  );
}
