"use client";

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowDownToLine,
  ArrowLeft,
  ArrowUpFromLine,
  Clock,
  Cpu,
  type LucideIcon,
  RefreshCw,
  Square,
  Wrench,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TurnStatusPip } from "@/components/logs/TurnStatusPip";
import { TraceTimeline } from "@/components/logs/TraceTimeline";
import { TraceEventDetail, TraceMetadata } from "@/components/logs/TraceEventDetail";
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
export default function LogDetailPage({ params }: { params: { turnId: string } }) {
  const { turnId } = params;
  const router = useRouter();
  const [detail, setDetail] = useState<TraceDetailDTO | null>(null);
  const [selected, setSelected] = useState<TraceEventDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [mobileShowDetail, setMobileShowDetail] = useState(false);

  const handleSelect = useCallback((e: TraceEventDTO) => {
    setSelected(e);
    setMobileShowDetail(true);
  }, []);

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

  const hasTokens = !!(turn?.input_tokens || turn?.output_tokens);

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
              <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                {turn.model && (
                  <MetricChip icon={Cpu} title="Model">
                    <span className="break-all">{turn.model}</span>
                  </MetricChip>
                )}
                {turn.session_name && (
                  <MetricChip title="Session">
                    <span className="truncate">{turn.session_name}</span>
                  </MetricChip>
                )}
                {turn.tool_call_count > 0 && (
                  <MetricChip icon={Wrench} title="Tool calls">
                    {turn.tool_call_count}
                  </MetricChip>
                )}
                {hasTokens && (
                  <MetricChip icon={ArrowDownToLine} title="Input tokens (prompt sent to model)">
                    {turn.input_tokens.toLocaleString()}
                  </MetricChip>
                )}
                {hasTokens && (
                  <MetricChip icon={ArrowUpFromLine} title="Output tokens (model reply)">
                    {turn.output_tokens.toLocaleString()}
                  </MetricChip>
                )}
                {latencyLabel && (
                  <MetricChip icon={Clock} title="Latency">
                    {latencyLabel}
                  </MetricChip>
                )}
                {turn.stop_reason && (
                  <MetricChip icon={Square} title="Stop reason">
                    {turn.stop_reason}
                  </MetricChip>
                )}
              </div>
              {turn.error && (
                <div className="rounded-md border border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger break-words">
                  {turn.error}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Body — flex column. Headers row + body grid both share the same
           `lg:grid-cols-[240px_minmax(0,1fr)_280px]` template so the seamless
           border-b under the header row aligns perfectly with the columns
           underneath. */}
        <div className="flex min-h-0 flex-1 flex-col px-3 sm:px-4">
          {!detail && !loading && (
            <div className="mt-3 rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
              Turn not found.
            </div>
          )}

          {detail && (
            <>
              {/* Header row — one parent, one border-b: seamless across both columns. */}
              <div className="grid grid-cols-1 gap-x-4 border-b border-border pb-2 pt-3 lg:grid-cols-[240px_minmax(0,1fr)_280px]">
                {/* Timeline header */}
                <div
                  className={cn(
                    "flex items-center gap-2",
                    mobileShowDetail ? "hidden lg:flex" : "flex",
                  )}
                >
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
                {/* Event header (body column) */}
                <div
                  className={cn(
                    "flex flex-wrap items-center gap-2",
                    mobileShowDetail ? "flex" : "hidden lg:flex",
                  )}
                >
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => setMobileShowDetail(false)}
                    className="-ml-2 h-7 shrink-0 gap-1 px-2 text-muted-foreground hover:text-foreground lg:hidden"
                  >
                    <ArrowLeft className="size-4" />
                    <span className="text-xs">Timeline</span>
                  </Button>
                  {selected ? (
                    <>
                      <Badge variant="secondary" className="h-5 font-mono text-[10px] uppercase">
                        {selected.kind}
                      </Badge>
                      {selected.tool_name && (
                        <span className="font-mono text-xs text-foreground break-all">
                          {selected.tool_name}
                        </span>
                      )}
                    </>
                  ) : (
                    <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                      event
                    </span>
                  )}
                </div>
                {/* Metadata header (right column, lg+ only) */}
                <div className="hidden lg:flex lg:items-center lg:gap-2">
                  <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                    metadata
                  </span>
                </div>
              </div>

              {/* Body grid — same column template. Each panel has its own scroll. */}
              <div className="grid min-h-0 flex-1 grid-cols-1 gap-x-4 pb-3 pt-2 lg:grid-cols-[240px_minmax(0,1fr)_280px]">
                <aside
                  className={cn(
                    "flex min-w-0 min-h-0 flex-col",
                    mobileShowDetail ? "hidden lg:flex" : "flex",
                  )}
                >
                  <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1 [scrollbar-gutter:stable] scroll-touch [overscroll-behavior:contain]">
                    <TraceTimeline
                      events={events}
                      selectedId={selected?.id ?? null}
                      onSelect={handleSelect}
                    />
                  </div>
                </aside>
                <section
                  className={cn(
                    "flex min-w-0 min-h-0 flex-col",
                    mobileShowDetail ? "flex" : "hidden lg:flex",
                  )}
                >
                  <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1 [scrollbar-gutter:stable] scroll-touch [overscroll-behavior:contain]">
                    <TraceEventDetail event={selected} />
                    {turn?.assistant_text && selected?.kind !== "assistant" && (
                      <div className="mt-4">
                        <div className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
                          Final reply
                        </div>
                        <pre className="overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-relaxed text-foreground">
                          {turn.assistant_text}
                        </pre>
                      </div>
                    )}
                    {/* Mobile-only metadata at bottom of body, since the right
                       column is desktop-only. */}
                    <div className="mt-4 lg:hidden">
                      <TraceMetadata event={selected} />
                    </div>
                  </div>
                </section>
                {/* Metadata column — desktop only. */}
                <aside className="hidden min-w-0 min-h-0 lg:flex lg:flex-col">
                  <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1 [scrollbar-gutter:stable] scroll-touch [overscroll-behavior:contain]">
                    <TraceMetadata event={selected} />
                  </div>
                </aside>
              </div>
            </>
          )}
        </div>
      </div>
    </TabFrame>
  );
}

function MetricChip({
  icon: Icon,
  title,
  children,
}: {
  icon?: LucideIcon;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className="inline-flex min-w-0 items-center gap-1 rounded-md border border-border/60 bg-muted/40 px-1.5 py-0.5 font-mono text-foreground/80"
    >
      {Icon && <Icon className="size-3 shrink-0 text-muted-foreground" aria-hidden />}
      <span className="truncate">{children}</span>
    </span>
  );
}
