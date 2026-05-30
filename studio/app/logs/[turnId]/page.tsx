"use client";

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  ArrowDownToLine,
  ArrowLeft,
  ArrowUpFromLine,
  Check,
  Clock,
  Copy,
  Cpu,
  type LucideIcon,
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

/* /logs/[turnId] - full timeline + per-event detail.
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
  const [copied, setCopied] = useState(false);

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

  const onCopyRun = useCallback(async () => {
    if (!detail) return;
    const text = serializeTurnForPaste(detail);
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* If clipboard write fails (older Safari, denied permission), open
       * the serialized text in a new window so the boss can copy it
       * manually. Better than a silent no-op. */
      const w = window.open("", "_blank");
      if (w) {
        w.document.body.innerText = text;
      }
    }
  }, [detail]);

  // Latency chip — three states, each with honest framing:
  //   • finished turn → "1m 30s" labelled "Latency"
  //   • in_flight ≤ 5min → "running 2m 15s" (the agent might actually be
  //     mid-turn; the server's hard per-turn timeout is 5 minutes)
  //   • in_flight > 5min → "stalled (5m+)" with warning treatment. Past
  //     the server timeout, an in_flight row is by definition stranded
  //     (boot recovery will close it on next core restart). Showing
  //     "103 minutes" would be a lie of the UI — the agent isn't doing
  //     anything, the row just never got its ended_at written.
  const STALL_SECONDS = 5 * 60;
  const latencyState = useMemo<{
    label: string;
    title: string;
    tone: "default" | "stalled";
  } | null>(() => {
    if (!turn?.latency_ms) return null;
    const s = turn.latency_ms / 1000;
    const formatElapsed = (sec: number) => {
      if (sec < 1) return `${Math.round(sec * 1000)}ms`;
      if (sec < 60) return `${sec.toFixed(1)}s`;
      return `${Math.round(sec / 60)}m ${Math.round(sec % 60)}s`;
    };
    if (turn.status === "in_flight") {
      if (s > STALL_SECONDS) {
        return {
          label: `stalled (${Math.round(STALL_SECONDS / 60)}m+)`,
          title:
            "Turn has been in_flight longer than the 5-minute server timeout — the agent loop almost certainly died and the row never got closed. Boot recovery will finalize it on the next core restart.",
          tone: "stalled",
        };
      }
      return {
        label: `running ${formatElapsed(s)}`,
        title: "Time elapsed since the turn started — still in flight",
        tone: "default",
      };
    }
    return { label: formatElapsed(s), title: "Latency", tone: "default" };
  }, [turn]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        {/* Header strip — three rows of decreasing visual weight:
              1. Toolbar (back + status pill + actions) — single-row chrome
              2. Title (the user's prompt, full prominence)
              3. Metadata chips (id, model, session, tools, tokens, latency)
            The old layout crowded id + status pip + uppercase status badge
            into the toolbar alongside the back button, which buried the
            actual prompt below a cluttered row. Now status lives as ONE
            tasteful pill on the right of the toolbar; the id moves to an
            eyebrow under the title; redundant "TURN" label is dropped. */}
        <div className="space-y-2.5 border-b px-3 py-3 sm:px-4">
          {/* Row 1 — toolbar. Back on left, status + actions on right. */}
          <div className="flex min-w-0 items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => router.push("/logs")}
              className="-ml-2 h-8 shrink-0 gap-1.5 px-2 text-muted-foreground hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              <span className="hidden sm:inline">Logs</span>
            </Button>
            <div className="ml-auto flex items-center gap-1">
              {turn && <TurnStatusPip status={turn.status} showLabel />}
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => void onCopyRun()}
                disabled={!detail}
                aria-label="Copy full run as markdown"
                title="Copy full run (paste to Jarvis or Claude)"
                className="h-8 w-8 shrink-0 px-0 text-muted-foreground hover:text-foreground"
              >
                {copied ? (
                  <Check className="size-4 text-success" aria-hidden />
                ) : (
                  <Copy className="size-4" aria-hidden />
                )}
              </Button>
            </div>
          </div>

          {turn && (
            <div className="space-y-1.5">
              {/* Row 2 — the prompt itself, the most important text on the page. */}
              <h1 className="line-clamp-2 break-words text-base font-semibold leading-snug text-foreground sm:text-lg">
                {turn.user_text || (
                  <span className="text-muted-foreground">(resumed turn — no fresh prompt)</span>
                )}
              </h1>
              {/* Row 3 — metadata chips. Turn-id chip leads so the boss
                  can copy it without scanning past the prompt. */}
              <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                <TurnIdChip turnId={turnId} />
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
                {latencyState && (
                  <MetricChip
                    icon={latencyState.tone === "stalled" ? AlertTriangle : Clock}
                    title={latencyState.title}
                    tone={latencyState.tone}
                  >
                    {latencyState.label}
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

        {/* Body - flex column. Headers row + body grid both share the same
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
              {/* Header row - one parent, one border-b: seamless across both columns. */}
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

              {/* Body grid - same column template. Each panel has its own scroll. */}
              <div className="grid min-h-0 flex-1 grid-cols-1 gap-x-4 pb-3 pt-2 lg:grid-cols-[240px_minmax(0,1fr)_280px]">
                <aside
                  className={cn(
                    "flex min-w-0 min-h-0 flex-col",
                    mobileShowDetail ? "hidden lg:flex" : "flex",
                  )}
                >
                  {/* pt-1 gives the first row's `ring-1` selection ring room to
                     render its top edge without being clipped by the
                     overflow-y-auto viewport at scrollTop=0. Without it the
                     top border of the selected user-prompt card disappears. */}
                  <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1 pt-1 [scrollbar-gutter:stable] scroll-touch [overscroll-behavior:contain]">
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
                {/* Metadata column - desktop only. */}
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

// serializeTurnForPaste flattens a TraceDetailDTO into a single Markdown
// blob suitable for pasting into another LLM session - Claude, Jarvis, or
// any future tooling. The shape mirrors what Jarvis itself returns from
// `trace_inspect(turn_id)` so the boss and an outside agent see the
// same content with the same labels.
function serializeTurnForPaste(detail: TraceDetailDTO): string {
  const { turn, events } = detail;
  const out: string[] = [];
  out.push(`# Turn ${turn.id}`);
  out.push("");
  out.push(`- session: ${turn.session_id}${turn.session_name ? ` (${turn.session_name})` : ""}`);
  if (turn.model) out.push(`- model: ${turn.model}`);
  out.push(`- status: ${turn.status}`);
  if (turn.stop_reason) out.push(`- stop_reason: ${turn.stop_reason}`);
  out.push(`- started_at: ${turn.started_at}`);
  if (turn.ended_at) out.push(`- ended_at: ${turn.ended_at}`);
  if (turn.latency_ms) out.push(`- latency_ms: ${turn.latency_ms}`);
  if (turn.input_tokens || turn.output_tokens) {
    out.push(`- tokens: in=${turn.input_tokens} out=${turn.output_tokens}`);
  }
  if (turn.tool_call_count) out.push(`- tool_calls: ${turn.tool_call_count}`);
  if (turn.error) out.push(`- error: ${turn.error}`);
  if (turn.summary) {
    out.push("");
    out.push("## summary");
    out.push(turn.summary);
  }
  if (turn.user_text) {
    out.push("");
    out.push("## user");
    out.push("```");
    out.push(turn.user_text);
    out.push("```");
  }
  out.push("");
  out.push(`## events (${events.length})`);
  for (const ev of events) {
    out.push("");
    const headerBits = [ev.kind];
    if (ev.tool_name) headerBits.push(ev.tool_name);
    if (ev.hook_name) headerBits.push(`hook:${ev.hook_name}`);
    out.push(`### [${ev.timestamp}] ${headerBits.join(" · ")}`);
    if (ev.tool_call_id) out.push(`- tool_call_id: ${ev.tool_call_id}`);
    if (ev.reason) out.push(`- reason: ${ev.reason}`);
    if (typeof ev.surprise === "number") out.push(`- surprise: ${ev.surprise.toFixed(3)}`);
    const blocks: Array<{ label: string; body: string }> = [];
    if (ev.input) blocks.push({ label: "input", body: ev.input });
    if (ev.expected) blocks.push({ label: "expected", body: ev.expected });
    if (ev.actual) blocks.push({ label: "actual", body: ev.actual });
    if (ev.output) blocks.push({ label: "output", body: ev.output });
    if (ev.error) blocks.push({ label: "error", body: ev.error });
    if (ev.raw_text) blocks.push({ label: "text", body: ev.raw_text });
    for (const b of blocks) {
      out.push("");
      out.push(`**${b.label}**`);
      out.push("```");
      out.push(b.body);
      out.push("```");
    }
  }
  if (turn.assistant_text) {
    out.push("");
    out.push("## assistant (final reply)");
    out.push("```");
    out.push(turn.assistant_text);
    out.push("```");
  }
  return out.join("\n");
}

function MetricChip({
  icon: Icon,
  title,
  tone = "default",
  children,
}: {
  icon?: LucideIcon;
  title?: string;
  tone?: "default" | "stalled";
  children: ReactNode;
}) {
  const stalled = tone === "stalled";
  return (
    <span
      title={title}
      className={cn(
        "inline-flex min-w-0 items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono",
        stalled
          ? "border-warning/40 bg-warning/10 text-warning"
          : "border-border/60 bg-muted/40 text-foreground/80",
      )}
    >
      {Icon && (
        <Icon
          className={cn(
            "size-3 shrink-0",
            stalled ? "text-warning" : "text-muted-foreground",
          )}
          aria-hidden
        />
      )}
      <span className="truncate">{children}</span>
    </span>
  );
}

// TurnIdChip renders the canonical turn UUID short-form with the full id
// in title + tap-to-copy, so the boss can drop the same identifier into
// chat with Jarvis ("trace_inspect <id>") or paste it back into Claude.
function TurnIdChip({ turnId }: { turnId: string }) {
  const [copied, setCopied] = useState(false);
  const short = turnId.length > 12 ? `${turnId.slice(0, 8)}…${turnId.slice(-4)}` : turnId;
  const onClick = async () => {
    try {
      await navigator.clipboard.writeText(turnId);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      /* ignore - most browsers grant clipboard on user gesture */
    }
  };
  return (
    <button
      type="button"
      onClick={() => void onClick()}
      title={`turn ${turnId} - tap to copy`}
      aria-label={`Copy turn id ${turnId}`}
      className="inline-flex items-center gap-1 rounded-md border border-border/60 bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] text-foreground/80 hover:bg-muted hover:text-foreground transition-colors"
    >
      <span className="select-all">{short}</span>
      {copied ? (
        <Check className="size-3 text-success" aria-hidden />
      ) : (
        <Copy className="size-3 text-muted-foreground" aria-hidden />
      )}
    </button>
  );
}
