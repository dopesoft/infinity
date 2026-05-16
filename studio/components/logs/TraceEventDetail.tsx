"use client";

import { Badge } from "@/components/ui/badge";
import type { TraceEventDTO } from "@/lib/api";

/* TraceEventDetail — the right pane body. Pinned header lives on the page.
 *
 * At xl+ the body is a two-column grid: wide content on the left (Input,
 * Output, Text, Payload — the stuff that benefits from horizontal room)
 * and a narrow metadata rail on the right (Event ID, Hook, Tool call ID,
 * Timestamp, etc — short key/value pairs the agent itself can see). Below
 * xl they stack with Metadata first so phone reading still works.
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

  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_220px]">
      <div className="min-w-0 space-y-4 xl:order-1">
        <Body event={event} />
      </div>
      <aside className="min-w-0 space-y-4 xl:order-2">
        <Metadata event={event} />
      </aside>
    </div>
  );
}

function Body({ event }: { event: TraceEventDTO }) {
  const hasInput = !!event.input;
  const hasRawText = !!event.raw_text;
  const hasPayload = !!event.payload && Object.keys(event.payload).length > 0;
  const isFallback = !["user", "assistant", "tool_call", "tool_result", "tool_error", "prediction", "gate"].includes(
    event.kind,
  );

  return (
    <>
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
          {typeof event.surprise === "number" && (
            <div className="flex items-center gap-2 text-xs">
              <span className="text-muted-foreground">Surprise</span>
              <Badge
                variant="secondary"
                className={
                  event.surprise >= 0.7
                    ? "bg-danger/15 text-danger"
                    : event.surprise >= 0.4
                      ? "bg-warning/15 text-warning"
                      : "bg-success/15 text-success"
                }
              >
                {event.surprise.toFixed(2)}
              </Badge>
            </div>
          )}
          <Pre label="Expected" body={event.expected || ""} />
          <Pre label="Actual" body={event.actual || ""} />
        </>
      )}

      {event.kind === "gate" && (
        <>
          {event.reason && <Pre label="Reason" body={event.reason} />}
          {event.payload?.status ? (
            <div className="text-xs text-muted-foreground">
              Status: <span className="text-foreground">{String(event.payload.status)}</span>
            </div>
          ) : null}
        </>
      )}

      {isFallback && hasRawText && <Pre label="Text" body={event.raw_text || ""} />}

      {event.error && event.kind !== "tool_error" && (
        <Pre label="Error" body={event.error} tone="danger" />
      )}
      {event.reason && event.kind !== "gate" && <Pre label="Reason" body={event.reason} />}

      {hasPayload && (
        <Pre label="Payload (raw)" body={JSON.stringify(event.payload, null, 2)} />
      )}
    </>
  );
}

function Metadata({ event }: { event: TraceEventDTO }) {
  const rows: Array<{ label: string; value: string; mono?: boolean }> = [];
  rows.push({ label: "Event ID", value: event.id, mono: true });
  rows.push({ label: "Kind", value: event.kind });
  rows.push({ label: "Source", value: event.source });
  if (event.hook_name) rows.push({ label: "Hook", value: event.hook_name, mono: true });
  if (event.tool_name) rows.push({ label: "Tool", value: event.tool_name, mono: true });
  if (event.tool_call_id) rows.push({ label: "Tool call ID", value: event.tool_call_id, mono: true });
  rows.push({ label: "Timestamp", value: event.timestamp, mono: true });
  if (typeof event.surprise === "number")
    rows.push({ label: "Surprise", value: event.surprise.toFixed(4), mono: true });

  return (
    <div>
      <div className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
        Metadata
      </div>
      <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-[11px]">
        <dl className="space-y-2">
          {rows.map((r) => (
            <div key={r.label}>
              <dt className="text-[9px] uppercase tracking-wide text-muted-foreground">
                {r.label}
              </dt>
              <dd
                className={
                  r.mono
                    ? "min-w-0 break-all font-mono text-foreground"
                    : "min-w-0 break-words text-foreground"
                }
                suppressHydrationWarning
              >
                {r.value}
              </dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  );
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
