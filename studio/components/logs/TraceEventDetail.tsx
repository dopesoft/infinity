"use client";

import { useMemo } from "react";
import { Badge } from "@/components/ui/badge";
import type { TraceEventDTO } from "@/lib/api";

/* TraceEventDetail — the right pane. Renders a single event's payload in
 * the form best for its kind:
 *   - tool_call → input args card
 *   - tool_result / tool_error → output card (preformatted)
 *   - prediction → expected vs actual pair + surprise score
 *   - gate → reason + status from the trust contract
 *   - user / assistant → raw text (markdown-y)
 *   - default → pretty JSON of the payload
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
  // Hooks must run on every render — early return lives below them.
  const ts = useMemo(() => {
    if (!event) return "";
    try {
      return new Date(event.timestamp).toLocaleString();
    } catch {
      return event.timestamp;
    }
  }, [event]);

  if (!event) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
        Select an event from the timeline to see its details.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 border-b border-border pb-2">
        <Badge variant="secondary" className="font-mono text-[10px] uppercase">
          {event.kind}
        </Badge>
        {event.tool_name && (
          <span className="font-mono text-xs text-foreground">{event.tool_name}</span>
        )}
        {event.tool_call_id && (
          <span className="font-mono text-[10px] text-muted-foreground">
            {event.tool_call_id.slice(0, 16)}
          </span>
        )}
        <span className="ml-auto text-[10px] text-muted-foreground" suppressHydrationWarning>
          {ts}
        </span>
      </div>

      {/* user / assistant — raw text */}
      {(event.kind === "user" || event.kind === "assistant") && (
        <Pre label="Text" body={event.raw_text || ""} />
      )}

      {/* tool_call */}
      {event.kind === "tool_call" && (
        <>
          <Pre label="Input" body={tryPretty(event.input || "")} />
        </>
      )}

      {/* tool_result / tool_error */}
      {(event.kind === "tool_result" || event.kind === "tool_error") && (
        <>
          {event.input && <Pre label="Input" body={tryPretty(event.input)} />}
          <Pre
            label={event.kind === "tool_error" ? "Error" : "Output"}
            body={event.output || event.raw_text || ""}
            tone={event.kind === "tool_error" ? "danger" : "default"}
          />
        </>
      )}

      {/* prediction */}
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

      {/* gate */}
      {event.kind === "gate" && (
        <>
          {event.tool_name && (
            <div className="text-xs text-muted-foreground">
              Title: <span className="text-foreground">{event.tool_name}</span>
            </div>
          )}
          {event.reason && <Pre label="Reason" body={event.reason} />}
          {event.payload?.status ? (
            <div className="text-xs text-muted-foreground">
              Status: <span className="text-foreground">{String(event.payload.status)}</span>
            </div>
          ) : null}
        </>
      )}

      {/* fallback for everything else — raw_text + payload */}
      {!["user", "assistant", "tool_call", "tool_result", "tool_error", "prediction", "gate"].includes(
        event.kind,
      ) && (
        <>
          {event.raw_text && <Pre label="Text" body={event.raw_text} />}
          {event.payload && Object.keys(event.payload).length > 0 && (
            <Pre label="Payload" body={JSON.stringify(event.payload, null, 2)} />
          )}
        </>
      )}
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
