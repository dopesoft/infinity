"use client";

import { CheckCircle2, Clock, Cpu, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { TraceEventDTO } from "@/lib/api";

/* TraceEventDetail — body content only. Pinned header lives on the page,
 * Metadata is exported separately as <TraceMetadata> so the page can render
 * it in its own grid column (LangSmith-style right rail).
 */
function tryPretty(maybeJSON: string): string {
  const s = (maybeJSON ?? "").trim();
  if (!s) return "";
  if (!(s.startsWith("{") || s.startsWith("["))) return s;
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

export function TraceEventDetail({ event }: { event: TraceEventDTO | null }) {
  if (!event) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
        Select an event from the timeline to see its details.
      </div>
    );
  }

  const hasInput = !!event.input;
  const hasRawText = !!event.raw_text;
  const hasPayload = !!event.payload && Object.keys(event.payload).length > 0;
  const isFallback = !["user", "assistant", "tool_call", "tool_result", "tool_error", "prediction", "gate"].includes(
    event.kind,
  );

  return (
    <div className="space-y-4">
      {(event.kind === "user" || event.kind === "assistant") && hasRawText && (
        <Pre label="Text" body={event.raw_text || ""} />
      )}

      {event.kind === "tool_call" && hasInput && (
        <Pre label="Input" body={tryPretty(event.input || "")} />
      )}

      {(event.kind === "tool_result" || event.kind === "tool_error") && (
        <>
          {hasInput && <Pre label="Input" body={tryPretty(event.input || "")} />}
          <Pre
            label={event.kind === "tool_error" ? "Error" : "Output"}
            body={event.output || event.raw_text || ""}
            tone={event.kind === "tool_error" ? "danger" : "default"}
          />
        </>
      )}

      {event.kind === "prediction" && (
        <>
          <Pre label="Expected" body={event.expected || ""} />
          <Pre label="Actual" body={event.actual || ""} />
        </>
      )}

      {event.kind === "gate" && event.reason && <Pre label="Reason" body={event.reason} />}

      {isFallback && hasRawText && <Pre label="Text" body={event.raw_text || ""} />}

      {event.error && event.kind !== "tool_error" && (
        <Pre label="Error" body={event.error} tone="danger" />
      )}
      {event.reason && event.kind !== "gate" && <Pre label="Reason" body={event.reason} />}

      {hasPayload && (
        <Pre label="Payload (raw)" body={JSON.stringify(event.payload, null, 2)} />
      )}
    </div>
  );
}

/* Right-rail metadata, LangSmith-style. Each field is a stacked
 * label/value pair with generous spacing. Categorical values render as
 * colored pills; identifiers as monospace text that can wrap.
 */
export function TraceMetadata({ event }: { event: TraceEventDTO | null }) {
  if (!event) {
    return (
      <div className="rounded-lg border border-border bg-muted/30 p-4 text-xs text-muted-foreground">
        No event selected.
      </div>
    );
  }

  const tone = statusToneFor(event.kind);
  const ts = formatTimestamp(event.timestamp);

  return (
    <div className="space-y-5 rounded-lg border border-border bg-muted/30 p-4">
      <Field label="Status">
        <StatusPill tone={tone.tone} icon={tone.icon}>
          {tone.label}
        </StatusPill>
      </Field>

      <Field label="Kind">
        <Pill>{event.kind}</Pill>
      </Field>

      <Field label="Source">
        <Pill variant={event.source === "observation" ? "muted" : "info"}>{event.source}</Pill>
      </Field>

      {event.hook_name && (
        <Field label="Hook">
          <Mono>{event.hook_name}</Mono>
        </Field>
      )}

      {event.tool_name && (
        <Field label="Tool">
          <Mono>{event.tool_name}</Mono>
        </Field>
      )}

      {event.tool_call_id && (
        <Field label="Tool call ID">
          <Mono className="break-all">{event.tool_call_id}</Mono>
        </Field>
      )}

      {typeof event.surprise === "number" && (
        <Field label="Surprise">
          <SurprisePill score={event.surprise} />
        </Field>
      )}

      <Field label="Timestamp">
        <Mono>{ts}</Mono>
      </Field>

      <Field label="Event ID">
        <Mono className="break-all text-muted-foreground">{event.id}</Mono>
      </Field>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
        {label}
      </div>
      <div className="text-sm text-foreground">{children}</div>
    </div>
  );
}

function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12px] text-foreground", className)}>{children}</span>;
}

function Pill({
  children,
  variant = "default",
}: {
  children: React.ReactNode;
  variant?: "default" | "muted" | "info";
}) {
  const tone =
    variant === "info"
      ? "border-info/30 bg-info/15 text-info"
      : variant === "muted"
        ? "border-border bg-muted text-muted-foreground"
        : "border-border bg-background text-foreground";
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider",
        tone,
      )}
    >
      {children}
    </span>
  );
}

function StatusPill({
  tone,
  icon: Icon,
  children,
}: {
  tone: "success" | "danger" | "warning" | "info" | "muted";
  icon: typeof CheckCircle2;
  children: React.ReactNode;
}) {
  const cls =
    tone === "success"
      ? "border-success/30 bg-success/10 text-success"
      : tone === "danger"
        ? "border-danger/30 bg-danger/10 text-danger"
        : tone === "warning"
          ? "border-warning/30 bg-warning/10 text-warning"
          : tone === "info"
            ? "border-info/30 bg-info/10 text-info"
            : "border-border bg-muted text-muted-foreground";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium",
        cls,
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      {children}
    </span>
  );
}

function SurprisePill({ score }: { score: number }) {
  const cls =
    score >= 0.7
      ? "border-danger/30 bg-danger/10 text-danger"
      : score >= 0.4
        ? "border-warning/30 bg-warning/10 text-warning"
        : "border-success/30 bg-success/10 text-success";
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2.5 py-1 font-mono text-xs",
        cls,
      )}
    >
      {score.toFixed(2)}
    </span>
  );
}

function statusToneFor(kind: string): {
  tone: "success" | "danger" | "warning" | "info" | "muted";
  icon: typeof CheckCircle2;
  label: string;
} {
  switch (kind) {
    case "tool_result":
      return { tone: "success", icon: CheckCircle2, label: "Success" };
    case "tool_error":
      return { tone: "danger", icon: XCircle, label: "Error" };
    case "gate":
      return { tone: "warning", icon: Clock, label: "Gated" };
    case "prediction":
      return { tone: "info", icon: Cpu, label: "Prediction" };
    case "user":
    case "assistant":
      return { tone: "info", icon: CheckCircle2, label: "Captured" };
    default:
      return { tone: "muted", icon: Clock, label: "Pending" };
  }
}

function formatTimestamp(ts: string): string {
  if (!ts) return "";
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

function Pre({
  label,
  body,
  tone = "default",
}: {
  label: string;
  body: string;
  tone?: "default" | "danger";
}) {
  if (!body) return null;
  return (
    <div>
      <div className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <pre
        className={
          tone === "danger"
            ? "max-h-[60vh] overflow-auto rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs leading-relaxed text-danger whitespace-pre-wrap break-words"
            : "max-h-[60vh] overflow-auto rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-relaxed text-foreground whitespace-pre-wrap break-words"
        }
      >
        {body}
      </pre>
    </div>
  );
}
